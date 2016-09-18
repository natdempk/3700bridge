package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
)

type Message struct {
	Source  string
	Dest    string
	Type    string // this could be an enum
	Message map[string]interface{}
}

type BPDU struct {
	RootID   string
	Cost     int
	BridgeID string
}

var lans = make(map[string]net.Conn)
var bestScoringBPDU BPDU
var designatedBridgeID string
var enabledLans = make(map[string]bool)

func main() {
	myBridgeID := os.Args[1]
	designatedBridgeID = myBridgeID
	bestScoringBPDU = BPDU{myBridgeID, 0, myBridgeID}
	lanIDs := os.Args[2:]

	for _, lanID := range lanIDs {
		conn, _ := net.Dial("unix", lanID)
		lans[lanID] = conn
		enabledLans[lanID] = true
	}

	fmt.Println("connected")
	testBPDUString := `{"source":"02a1", "dest":"ffff", "type": "bpdu", "message":{"id":"92b4", "root":"02a1", "cost":3}}`
	testBPDU := []byte(testBPDUString)
	//testDataString := `{"source":"28aa", "dest":"97bf", "type": "data", "message":{"id": 17}}`

	unknownMessage := parseMessage(testBPDU)
	if unknownMessage.Type == "bpdu" {
		updateBPDU(unknownMessage, socketLan)
	} else {
		//sendData(uknownMessage, incomingSocketId)
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

	for _, conn := range lans {
		bytes, _ := json.Marshal(&message)
		fmt.Fprintf(conn, string(bytes))
	}

}

func updateBPDU(message Message, socketLan string) {
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
		// do nothing
		enabledLans[socketLan] = true
	} else {
		bestScoringBPDU.Cost += 1
		bestScoringBPDU.RootID = receivedBPDU.RootID
		designatedBridgeID = receivedBPDU.BridgeID
		//enabledLans[socketLan] = true

		// TODO: figure out port shutdown logic
	}

}

func parseMessage(bytes []byte) (message Message) {
	message = Message{}
	json.Unmarshal(bytes, &message)
	return
}
