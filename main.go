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
	Source  string                 `json:"source`
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
	//Address   string
	LANID     string
	CreatedAt time.Time
}

type IncomingBPDU struct {
	BPDU  Message
	LanID string
}

// maps lanIds to Conns
var LANConns = make(map[string]net.Conn)

// has best scoring root, and path cost
// always our bridgeID because this is the BPDU we broadcast to others
var bestScoringBPDU BPDU

// this is the next hop bridge to root
var designatedBridgeID string

// are these enabled lan connections or enabled bridgeIdConnections? I think bridgeIDs
// then we need to keep the forwarding table of LanID -> bridgeId for that connection
var enabledLANConns = make(map[string]bool)

//var forwardingTable []LANForwardingEntry
var fowardingTableMap = make(map[string]LANForwardingEntry)

var receivedBPDUs = make(chan IncomingBPDU)

func padLANID(lanID string) (paddedID string) {
	return fmt.Sprintf("%c", '\x00') + lanID + strings.Repeat(fmt.Sprintf("%c", '\x00'), 106-len(lanID))
}

func main() {
	myBridgeID := os.Args[1]
	designatedBridgeID = myBridgeID
	bestScoringBPDU = BPDU{myBridgeID, 0, myBridgeID}
	lanIDs := os.Args[2:]
	fmt.Println("lanIDs")
	fmt.Println(lanIDs)

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
		fmt.Printf("Designated port: %s/%s\n", bestScoringBPDU.BridgeID, lanID)
	}

	//	fmt.Println("LAN CONNS")
	// fmt.Println(LANConns)
	fmt.Println("broadcast bpdu")

	broadcastBPDU(bestScoringBPDU)

	for lanID, LANConn := range LANConns {
		fmt.Println("creating goroutine ", lanID)
		go func() {
			fmt.Println("CREATED GOROUTINE, trying to read messages ", lanID)
			for {
				var buf []byte
				bitscopied, err := LANConn.Read(buf[:])
				if err {
					fmt.Println(err)
				}

				fmt.Println("Parsed message successfully")
				var unknownMessage Message

				if err := json.Unmarshal(buf, &unknownMessage); err != nil {
					fmt.Printf("horrible error\n")
					panic(err)
				}

				fmt.Println("unknownMessage")
				fmt.Println(unknownMessage)

				if unknownMessage.Type == "bpdu" {
					fmt.Println("bpduuuuuuuuuu")
					// updateBPDU(unknownMessage, lanID)
					receivedBPDUs <- IncomingBPDU{BPDU: unknownMessage,
						LanID: lanID,
					}

				} else {
					fmt.Printf("Received message %s on port %s from %s to %s\n", unknownMessage.Message["id"], lanID, unknownMessage.Source, unknownMessage.Dest)
					sendData(unknownMessage, lanID)
				}
			}
		}()
		fmt.Println("post created goroutine ", lanID)
	}

	for {
		fmt.Println("looping waiting on BPDUs")
		incomingBPDU := <-receivedBPDUs
		updateBPDU(incomingBPDU.BPDU, incomingBPDU.LanID)
	}
}

func sendData(message Message, incomingLan string) {
	if val, ok := fowardingTableMap[message.Dest]; ok && time.Since(val.CreatedAt).Seconds() < 5.0 {
		conn, _ := LANConns[val.LANID]
		bytes, _ := json.Marshal(message)
		fmt.Fprintf(conn, string(bytes))
	} else { // we don't know where to send our message, so we send it everywhere except the incomping port
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
	dataMessage["id"] = bestScoringBPDU.BridgeID
	dataMessage["root"] = bestScoringBPDU.RootID
	dataMessage["cost"] = bestScoringBPDU.Cost

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
		fmt.Fprintf(conn, string(bytes))
		fmt.Println("sent BPDU to ", lanID)
	}
	fmt.Println("done sending bpdu")
}

func updateBPDU(message Message, incomingLan string) {
	BPDUMessage := message.Message
	receivedBPDU := BPDU{RootID: BPDUMessage["root"].(string),
		Cost:     BPDUMessage["cost"].(int),
		BridgeID: BPDUMessage["id"].(string),
	}

	if (bestScoringBPDU.RootID < receivedBPDU.RootID) ||
		(bestScoringBPDU.RootID == receivedBPDU.RootID &&
			bestScoringBPDU.Cost < receivedBPDU.Cost) ||
		(bestScoringBPDU.RootID == receivedBPDU.RootID &&
			bestScoringBPDU.Cost < receivedBPDU.Cost &&
			bestScoringBPDU.BridgeID < receivedBPDU.BridgeID) {
		fmt.Printf("Designated port: %s/%s\n", bestScoringBPDU.BridgeID, incomingLan)
		// do nothing
	} else {
		bestScoringBPDU.Cost++

		if bestScoringBPDU.RootID != receivedBPDU.RootID {
			bestScoringBPDU.RootID = receivedBPDU.RootID
			fmt.Printf("New root: %s/%s\n", bestScoringBPDU.BridgeID, incomingLan)
		}

		designatedBridgeID = receivedBPDU.BridgeID

		broadcastBPDU(bestScoringBPDU)
	}

	// equidistant case
	if bestScoringBPDU.RootID == receivedBPDU.RootID &&
		bestScoringBPDU.Cost == receivedBPDU.Cost &&
		bestScoringBPDU.BridgeID > receivedBPDU.BridgeID {
		// same root, and taking that root would have the same cost as our cost
		// if our bridge id is higher than theirs disable the port for lan traffic
		enabledLANConns[incomingLan] = false
		fmt.Printf("Disabled port: %s/%s\n", bestScoringBPDU.BridgeID, incomingLan)

	} else if bestScoringBPDU.RootID == receivedBPDU.RootID &&
		bestScoringBPDU.Cost == receivedBPDU.Cost+1 &&
		designatedBridgeID < receivedBPDU.BridgeID {
		// we have a 1 higher cost, same root id, our designated bridge id is higher than theirs
		enabledLANConns[incomingLan] = false
		fmt.Printf("Disabled port: %s/%s\n", bestScoringBPDU.BridgeID, incomingLan)
	} else { // enable the port
		enabledLANConns[incomingLan] = true
	}

}

func parseMessage(bytes []byte) (message Message) {
	message = Message{}
	json.Unmarshal(bytes, &message)
	return
}
