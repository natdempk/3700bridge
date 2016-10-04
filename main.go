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
	LANID string
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

// maps LANIDs to Conns
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
// then we need to keep the forwarding table of LANID -> bridgeId for that connection
var enabledLANConns = make(map[string]bool)
var baseLANs = []string{}

//var forwardingTable []LANForwardingEntry
var forwardingTableMap = make(map[string]LANForwardingEntry)

var BPDUTable = make(map[BPDUTableKey]BPDUTableEntry)
var receivedBPDUs = make(chan IncomingBPDU)

func padLANID(LANID string) (paddedID string) {
	return fmt.Sprintf("%c", '\x00') + LANID + strings.Repeat(fmt.Sprintf("%c", '\x00'), 106-len(LANID))
}

func main() {
	myBridgeID := os.Args[1]
	designatedBridgeID = myBridgeID
	outgoingBPDU = BPDU{myBridgeID, 0, myBridgeID}
	initialBPDU = BPDU{myBridgeID, 0, myBridgeID}
	LANIDs := os.Args[2:]
	fmt.Printf("Bridge %s starting up\n", myBridgeID)

	for _, LANID := range LANIDs {
		// ignore duplicate LANIDs
		if _, ok := LANConns[LANID]; ok {
			continue
		}

		conn, err := net.Dial("unixpacket", padLANID(LANID))
		if err != nil {
			fmt.Println("TERRIBLE ERROR")
			fmt.Println(err)
		}
		LANConns[LANID] = conn
		enabledLANConns[LANID] = true
		baseLANs = append(baseLANs, LANID)
		// fmt.Printf("Designated port: %s/%s\n", bestScoringBPDU.BridgeID, LANID)
	}

	go func() {
		for {
			broadcastBPDU(outgoingBPDU)
			time.Sleep(500 * time.Millisecond)
		}
	}()

	for LANID, LANConn := range LANConns {
		go listenForMessage(LANID, LANConn)
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
			LANID:    incomingBPDU.LANID,
		}

		potentialTableEntry := BPDUTableEntry{
			BPDU:        receivedBPDU,
			IncomingLAN: incomingBPDU.LANID,
			CreatedAt:   time.Now(),
		}

		// check for previous entry with lesser LANID
		BPDUTable[tableKey] = potentialTableEntry

		var currentBestBPDU BPDU

		newEnabledLANConns, newRootPort, currentBestBPDU := updateBPDU()

		if compare(newEnabledLANConns, enabledLANConns) ||
			newRootPort != rootPort {
			for k := range forwardingTableMap {
				delete(forwardingTableMap, k)
			}
		}

		if newRootPort != rootPort {
			fmt.Printf("New Root: %s/%s\n", initialBPDU.BridgeID, newRootPort)
		}

		for port, enabled := range newEnabledLANConns {
			// if newly enabled or disabled
			if enabled != enabledLANConns[port] && port != newRootPort {
				if enabled {
					fmt.Printf("Designated Port: %s/%s\n", initialBPDU.BridgeID, port)
				} else {
					fmt.Printf("Disabled Port: %s/%s\n", initialBPDU.BridgeID, port)
				}
			}
		}
		enabledLANConns = newEnabledLANConns
		rootPort = newRootPort

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
func listenForMessage(LANID string, LANConn net.Conn) {
	d := json.NewDecoder(LANConn)
	for {

		var unknownMessage Message
		err := d.Decode(&unknownMessage)
		if err != nil {
			fmt.Printf("horrible error")
			panic(err)
		}

		if unknownMessage.Type == "bpdu" {
			// updateBPDU(unknownMessage, LANID)
			receivedBPDUs <- IncomingBPDU{BPDU: unknownMessage,
				LANID: LANID,
			}

		} else {
			fmt.Printf("Received message %v on port %s from %s to %s\n", unknownMessage.Message["id"], LANID, unknownMessage.Source, unknownMessage.Dest)
			if enabledLANConns[LANID] {
				forwardingTableMap[unknownMessage.Source] = LANForwardingEntry{
					LANID:     LANID,
					CreatedAt: time.Now(),
				}

				sendData(unknownMessage, LANID)
			}
		}
	}
}

func sendData(message Message, incomingLan string) {
	if tableEntry, ok := forwardingTableMap[message.Dest]; ok && time.Since(tableEntry.CreatedAt).Seconds() < 5.0 {
		if tableEntry.LANID != incomingLan { // if where we would forward to is where we got the message from
			conn, _ := LANConns[tableEntry.LANID]
			bytes, _ := json.Marshal(message)
			fmt.Fprintf(conn, string(bytes))
			fmt.Printf("Forwarding message %s to port %s", message.Message["id"], tableEntry.LANID)
		} else {
			fmt.Printf("Not forwarding message %s", message.Message["id"])
		}
	} else { // we don't know where to send our message, so we send it everywhere except the incoming port
		fmt.Printf("Broadcasting message %s to all ports", message.Message["id"])
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

	for LANID, conn := range LANConns {
		bytes, err := json.Marshal(message)
		if err != nil {
			fmt.Println("marshal error")
			fmt.Println(err)
		}
		fmt.Println(LANID, "sent bpdu")
		fmt.Fprintf(conn, string(bytes))
	}
}

func updateBPDU() (enabledLANs map[string]bool, currentBestLANID string, currentBestBPDU BPDU) {
	//fmt.Println(receivedBPDU, incomingLan)

	currentBestBPDU = initialBPDU
	BPDULans := make(map[string][]BPDU)
	enabledLANs = make(map[string]bool)

	// initialize BPDULans
	for _, LANID := range baseLANs {
		BPDULans[LANID] = []BPDU{}
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
}

func lowestCost(BPDUList []BPDU, rootID string) BPDU {
	var lowestCostBPDU BPDU
	foundThing := false
	for _, b := range BPDUList {
		if b.RootID != rootID {
			continue
		}
		if !foundThing {
			lowestCostBPDU = b
			foundThing = true
		} else {
			if b.Cost < lowestCostBPDU.Cost ||
				(b.Cost == lowestCostBPDU.Cost &&
					b.BridgeID < lowestCostBPDU.BridgeID) {
				lowestCostBPDU = b
			}
		}
	}

	return lowestCostBPDU
}
