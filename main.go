package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"
)

var settings struct {
	ServerHost                 string
	ServerPort                 uint16
	ServerPassword             string
	AppID                      uint32
	ServerTimeoutSeconds       int
	RecTalkgroupID             uint32
	CallHangTimeSeconds        int
}

var loggedIn bool

type udpPacket struct {
	data []byte
	len  int
}

// receivePackets sends all received packets on the given connection to the given channel.
func receivePackets(conn net.Conn, recvUDP chan udpPacket) {
	for {
		buffer := make([]byte, 128)
		readBytes, err := conn.Read(buffer)
		if err != nil {
			log.Fatal(err)
		}
		recvUDP <- udpPacket{data: buffer, len: readBytes}
	}
}

// handlePacket returns true if given packet was valid.
func handlePacket(conn net.Conn, p *udpPacket) bool {
	var rd rewindData
	rb := bytes.NewReader(p.data)
	binary.Read(rb, binary.LittleEndian, &rd)
	payload := make([]byte, rd.PayloadLength)
	pl, err := rb.Read(payload)
	if err != nil || pl != int(rd.PayloadLength) {
		log.Println("invalid payload length, dropping packet")
		return false
	}
	switch rd.PacketType {
	case rewindPacketTypeKeepAlive:
		if !loggedIn {
			// Requesting super headers.
			sendSubscription(conn, settings.RecTalkgroupID, rewindSessionTypeGroupVoice);
		}
	case rewindPacketTypeConfiguration:
		log.Println("got configuration ack")
		if !loggedIn {
			// Subscribing to the requested TG.
			sendSubscription(conn, settings.RecTalkgroupID, rewindSessionTypeGroupVoice)
		}
	case rewindPacketTypeSubscription:
		log.Println("got subscription ack")
		if !loggedIn {
			log.Println("logged in")
			loggedIn = true
		}
	case rewindPacketTypeReport:
		log.Println("server report: ", pl)
	case rewindPacketTypeChallenge:
		log.Println("got challenge")
		loggedIn = false
		sendChallengeResponse(conn, sha256.Sum256(append(payload, []byte(settings.ServerPassword)...)))
	case rewindPacketTypeFailureCode:
		log.Println("got failure code: ", pl)
	case rewindPacketTypeDMRAudioFrame:
		//log.Println("got dmr audio frame")
		handleDMRAudioFrame(payload)
	case rewindPacketTypeClose:
		log.Fatal("got close request")
	default:
		return false
	}
	return true
}

func main() {
	sigs := make(chan os.Signal, 1)

	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGPIPE)

	configFileName := "config.json"

	flag.StringVar(&configFileName, "c", configFileName, "config file to use, default: config.json")
	flag.Parse()

	cf, err := os.Open(configFileName)
	if err != nil {
		log.Fatal(err)
	}

	if err = json.NewDecoder(cf).Decode(&settings); err != nil {
		log.Fatal("error parsing config file:", err.Error())
	}

	serverHostPort := fmt.Sprintf("%s:%d", settings.ServerHost, settings.ServerPort)
	log.Println("using server and port", serverHostPort)
	conn, err := net.Dial("udp", serverHostPort)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	recvUDP := make(chan udpPacket)
	go receivePackets(conn, recvUDP)

	log.Println("starting listening loop")

	var timeLastSentKeepalive time.Time
	var timeLastValidPacket time.Time

	go func() {
		<-sigs
		sendClose(conn)
		os.Exit(0)
	}()

	for {
		timeDiff := time.Since(timeLastSentKeepalive)
		if timeDiff.Seconds() >= 5 {
			sendKeepalive(conn)
			timeLastSentKeepalive = time.Now()
		}

		select {
		case p := <-recvUDP:
			if p.len >= len(rewindProtocolSign) && bytes.Compare(p.data[:len(rewindProtocolSign)], []byte(rewindProtocolSign)) == 0 {
				if handlePacket(conn, &p) {
					timeLastValidPacket = time.Now()
				}
			}
		case <-time.After(time.Second * 5):
		}

		timeDiff = time.Since(timeLastValidPacket)
		if timeDiff.Seconds() >= float64(settings.ServerTimeoutSeconds) {
			log.Fatal("timeout, disconnected")
		}

	}
}

func handleDMRAudioFrame(payload []byte) {
        binary.Write(os.Stdout, binary.LittleEndian, payload)
}
