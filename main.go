package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"time"
)

type Message struct {
	Source  string                 `json:"source"`
	Dest    string                 `json:"dest"`
	Type    string                 `json:"type"` // this could be an enum
	Message map[string]interface{} `json:"message"`
}

type BPDU struct {
	RootID   string `json:"root"`
	Cost     int    `json:"cost"`
	BridgeID string `json:"id"`
}

type LANForwardingEntry struct {
	LANID     string
	CreatedAt time.Time
}

type IncomingBPDU struct {
	BPDU  Message
	LanID string
}

type BPDUTableEntry struct {
	BPDU        BPDU
	IncomingLAN string
	CreatedAt   time.Time
}

type BPDUTableKey struct {
	BridgeID string
	LANID    string
}

// maps lanIds to Conns
var LANConns = make(map[string]net.Conn)

// has best scoring root, and path cost
// always our bridgeID because this is the BPDU we broadcast to others
//var bestScoringBPDU BPDU
var outgoingBPDU BPDU
var initialBPDU BPDU

// this is the next hop bridge to root
var designatedBridgeID string
var rootPort string

// are these enabled lan connections or enabled bridgeIdConnections? I think bridgeIDs
// then we need to keep the forwarding table of LanID -> bridgeId for that connection
var enabledLANConns = make(map[string]bool)
var baseLANs = []string{}

//var forwardingTable []LANForwardingEntry
var forwardingTableMap = make(map[string]LANForwardingEntry)

var BPDUTable = make(map[BPDUTableKey]BPDUTableEntry)
var receivedBPDUs = make(chan IncomingBPDU)

func padLANID(lanID string) (paddedID string) {
	return fmt.Sprintf("%c", '\x00') + lanID + strings.Repeat(fmt.Sprintf("%c", '\x00'), 106-len(lanID))
}

func main() {
	myBridgeID := os.Args[1]
	designatedBridgeID = myBridgeID
	outgoingBPDU = BPDU{myBridgeID, 0, myBridgeID}
	initialBPDU = BPDU{myBridgeID, 0, myBridgeID}
	lanIDs := os.Args[2:]
	fmt.Printf("Bridge %s starting up\n", myBridgeID)

	for _, lanID := range lanIDs {
		// ignore duplicate lanIDs
		if _, ok := LANConns[lanID]; ok {
			continue
		}

		conn, err := net.Dial("unixpacket", padLANID(lanID))
		if err != nil {
			fmt.Println("TERRIBLE ERROR")
			fmt.Println(err)
		}
		LANConns[lanID] = conn
		enabledLANConns[lanID] = true
		baseLANs = append(baseLANs, lanID)
		// fmt.Printf("Designated port: %s/%s\n", bestScoringBPDU.BridgeID, lanID)
	}

	go func() {
		for {
			broadcastBPDU(outgoingBPDU)
			time.Sleep(500 * time.Millisecond)
		}
	}()

	for lanID, LANConn := range LANConns {
		go listenForMessage(lanID, LANConn)
	}

	for {
		incomingBPDU := <-receivedBPDUs

		BPDUMessage := incomingBPDU.BPDU.Message
		cost := BPDUMessage["cost"].(float64)
		intCost := int(cost)
		receivedBPDU := BPDU{RootID: BPDUMessage["root"].(string),
			Cost:     intCost,
			BridgeID: BPDUMessage["id"].(string),
		}

		// build potential table entry
		tableKey := BPDUTableKey{
			BridgeID: receivedBPDU.BridgeID,
			LANID:    incomingBPDU.LanID,
		}

		val, ok := BPDUTable[tableKey]
		potentialTableEntry := BPDUTableEntry{
			BPDU:        receivedBPDU,
			IncomingLAN: incomingBPDU.LanID,
			CreatedAt:   time.Now(),
		}

		// check for previous entry with lesser LanID
		if ok && (tableKey.LANID <= val.IncomingLAN) {
			BPDUTable[tableKey] = potentialTableEntry
		} else if !ok { // or if we have no entry
			BPDUTable[tableKey] = potentialTableEntry
		}

		var currentBestBPDU BPDU

		enabledLANConns, rootPort, currentBestBPDU = updateBPDU()

		bestCost := currentBestBPDU.Cost
		if initialBPDU.RootID != currentBestBPDU.RootID {
			bestCost++
		}

		outgoingBPDU = BPDU{
			RootID:   currentBestBPDU.RootID,
			Cost:     bestCost,
			BridgeID: initialBPDU.BridgeID,
		}
	}
}

func listenForMessage(lanID string, LANConn net.Conn) {
	d := json.NewDecoder(LANConn)
	for {

		var unknownMessage Message
		err := d.Decode(&unknownMessage)
		if err != nil {
			fmt.Printf("horrible error")
			panic(err)
		}

		if unknownMessage.Type == "bpdu" {
			// updateBPDU(unknownMessage, lanID)
			receivedBPDUs <- IncomingBPDU{BPDU: unknownMessage,
				LanID: lanID,
			}

		} else {
			fmt.Println(enabledLANConns)
			// fmt.Printf("Received message %v on port %s from %s to %s\n", unknownMessage.Message["id"], lanID, unknownMessage.Source, unknownMessage.Dest)
			if enabledLANConns[lanID] {
				forwardingTableMap[unknownMessage.Source] = LANForwardingEntry{
					LANID:     lanID,
					CreatedAt: time.Now(),
				}

				sendData(unknownMessage, lanID)
			}
		}
	}
}

func sendData(message Message, incomingLan string) {
	fmt.Println(enabledLANConns)
	if val, ok := forwardingTableMap[message.Dest]; ok && time.Since(val.CreatedAt).Seconds() < 5.0 {
		if val.LANID != incomingLan { // if where we would forward to is where we got the message from
			conn, _ := LANConns[val.LANID]
			bytes, _ := json.Marshal(message)
			fmt.Fprintf(conn, string(bytes))
		}
	} else { // we don't know where to send our message, so we send it everywhere except the incoming port
		// for each active port, send the message
		for k, v := range enabledLANConns {
			if k != incomingLan && v {
				conn, _ := LANConns[k]
				bytes, _ := json.Marshal(message)
				fmt.Fprintf(conn, string(bytes))
			}
		}
	}
}

func broadcastBPDU(bpdu BPDU) {
	dataMessage := make(map[string]interface{})
	dataMessage["id"] = outgoingBPDU.BridgeID
	dataMessage["root"] = outgoingBPDU.RootID
	dataMessage["cost"] = outgoingBPDU.Cost

	message := Message{Source: bpdu.BridgeID,
		Dest:    "ffff",
		Type:    "bpdu",
		Message: dataMessage}

	for lanID, conn := range LANConns {
		bytes, err := json.Marshal(message)
		if err != nil {
			fmt.Println("marshal error")
			fmt.Println(err)
		}
		fmt.Println(lanID, "sent bpdu")
		fmt.Fprintf(conn, string(bytes))
	}
}

func min(a, b int) int {
	if a < b {
		return a

	}
	return b

}

func lowestCost(BPDUList []BPDU, rootID string) BPDU {
	cost := BPDUList[0].Cost
	for _, b := range BPDUList[1:] {
		if b.RootID != rootID {
			continue
		}

		cost = min(cost, b.Cost)
	}

	lowestCostBPDU := BPDUList[0]

	for _, b := range BPDUList {
		if b.RootID != rootID {
			continue
		}

		if b.Cost == cost {
			if b.BridgeID < lowestCostBPDU.BridgeID {
				lowestCostBPDU = b
			}
		}
	}

	return lowestCostBPDU
}

//func updateBPDU(message Message, incomingLan string) {
func updateBPDU() (enabledLANs map[string]bool, currentBestLANID string, currentBestBPDU BPDU) {
	//fmt.Println(receivedBPDU, incomingLan)

	currentBestBPDU = initialBPDU
	BPDULans := make(map[string][]BPDU)
	enabledLANs = make(map[string]bool)

	// initialize BPDULans
	for _, lanID := range baseLANs {
		BPDULans[lanID] = []BPDU{}
	}

	// delete expired entries
	for key, tableEntry := range BPDUTable {
		if time.Since(tableEntry.CreatedAt).Seconds()*1000.0 > 750.0 {
			delete(BPDUTable, key)
		} else { // compare to find best one
			BPDULans[tableEntry.IncomingLAN] = append(BPDULans[tableEntry.IncomingLAN], tableEntry.BPDU)

			if (currentBestBPDU.RootID < tableEntry.BPDU.RootID) ||
				(currentBestBPDU.RootID == tableEntry.BPDU.RootID &&
					currentBestBPDU.Cost < tableEntry.BPDU.Cost) ||
				(currentBestBPDU.RootID == tableEntry.BPDU.RootID &&
					currentBestBPDU.Cost == tableEntry.BPDU.Cost &&
					currentBestBPDU.BridgeID < tableEntry.BPDU.BridgeID) {
				// do nothing
			} else {
				currentBestBPDU = tableEntry.BPDU
				currentBestLANID = tableEntry.IncomingLAN
			}
		}
	}

	for LANID, BPDUs := range BPDULans {
		if len(BPDUs) == 0 {
			// enable LANs we got not BPDUs from
			enabledLANs[LANID] = true
		} else {
			lowestCostBPDU := lowestCost(BPDUs, currentBestBPDU.RootID)

			if currentBestBPDU.Cost+1 < lowestCostBPDU.Cost ||
				(currentBestBPDU.Cost+1 == lowestCostBPDU.Cost &&
					initialBPDU.BridgeID < lowestCostBPDU.BridgeID) {
				// we are designated
				enabledLANs[LANID] = true
			} else {
				enabledLANs[LANID] = false
			}
		}
	}

	// enable our root port
	if currentBestLANID != "" {
		enabledLANs[currentBestLANID] = true
	}

	return

	//////////////////////////////////////////////

	// equidistant lan case
	//if bestScoringBPDU.RootID == receivedBPDU.RootID &&
	//bestScoringBPDU.Cost == receivedBPDU.Cost &&
	//bestScoringBPDU.BridgeID > receivedBPDU.BridgeID {
	//// same root, and taking that root would have the same cost as our cost
	//// if our bridge id is higher than theirs disable the port for lan traffic
	//enabledLANConns[incomingLan] = false
	//fmt.Printf("Disabled port part 1: %s/%s\n", bestScoringBPDU.BridgeID, incomingLan)
	//// equidistant bridge
	//} else if bestScoringBPDU.RootID == receivedBPDU.RootID &&
	//bestScoringBPDU.Cost == receivedBPDU.Cost+1 &&
	//rootPort < incomingLan {
	//// we have the same cost, same root id, our designated bridge id is higher than or equal to theirs
	//enabledLANConns[incomingLan] = false
	//fmt.Printf("Disabled port part 2: %s/%s\n", bestScoringBPDU.BridgeID, incomingLan)
	//} else { // enable the port
	//// enabledLANConns[incomingLan] = true
	//}

	//if (bestScoringBPDU.RootID < receivedBPDU.RootID) ||
	//(bestScoringBPDU.RootID == receivedBPDU.RootID &&
	//bestScoringBPDU.Cost < receivedBPDU.Cost) ||
	//(bestScoringBPDU.RootID == receivedBPDU.RootID &&
	//bestScoringBPDU.Cost == receivedBPDU.Cost &&
	//bestScoringBPDU.BridgeID < receivedBPDU.BridgeID) {
	//fmt.Printf("Designated port: %s/%s\n", bestScoringBPDU.BridgeID, incomingLan)
	//// do nothing
	//} else {
	//bestScoringBPDU.Cost = receivedBPDU.Cost + 1

	//if bestScoringBPDU.RootID != receivedBPDU.RootID {
	//bestScoringBPDU.RootID = receivedBPDU.RootID
	//fmt.Printf("New root: %s/%s\n", bestScoringBPDU.BridgeID, incomingLan)
	//}
	//if incomingLan < rootPort {
	//rootPort = incomingLan
	//enabledLANConns[incomingLan] = true
	//}
	//designatedBridgeID = receivedBPDU.BridgeID
	//}

}

//func parseMessage(bytes []byte) (message Message) {
//message = Message{}
//json.Unmarshal(bytes, message)
//return
//}
