package zabbix

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"
)

type packetStruct struct {
	version int8
	data    string
}

type callback func(key string, args []string) string

func handleConnection(c io.ReadWriteCloser, cb callback) {
	decodedRequest, err := decode(c)
	if err != nil {
		log.Printf("Unable to decode Zabbix packet: %v", err)
		c.Close()
		return
	}

	var answer packetStruct
	answer.data = cb(decodedRequest.data, []string{""})
	answer.version = decodedRequest.version

	var encodedAnswer []byte
	if answer.version == 1 {
		encodedAnswer, err = encodev1(answer)
	}
	if err != nil {
		log.Println(err)
		c.Close()
		return
	}

	_, err = c.Write(encodedAnswer)
	if err != nil {
		log.Printf("Answer writing failed: %v", err)
	}

	c.Close()
}

func decode(r io.Reader) (packetStruct, error) {
	packetHead := make([]byte, 13)
	_, err := r.Read(packetHead)
	if err != nil {
		return packetStruct{}, err
	}
	var decodedPacket packetStruct
	header := packetHead[0:4]
	buf := bytes.NewReader(packetHead[4:])

	if bytes.Compare(header, []byte("ZBXD")) != 0 {
		err = fmt.Errorf("wrong packet header")
		return decodedPacket, err
	}
	err = binary.Read(buf, binary.LittleEndian, &decodedPacket.version)
	if err != nil {
		err = fmt.Errorf("binary.Read failed for packet_version: %v", err)
		return decodedPacket, err
	}
	var dataLength int64
	err = binary.Read(buf, binary.LittleEndian, &dataLength)
	if err != nil {
		err = fmt.Errorf("binary.Read failed for packet_version: %v", err)
		return decodedPacket, err
	}
	packetData := make([]byte, dataLength)
	_, err = r.Read(packetData)
	if err != nil {
		err = fmt.Errorf("r.Read failed for data: %v", err)
		return decodedPacket, err
	}
	decodedPacket.data = string(packetData)
	return decodedPacket, err
}

func encodev1(decodedPacket packetStruct) ([]byte, error) {
	encodedPacket := make([]byte, 13+len(decodedPacket.data))

	copy(encodedPacket[0:4], []byte("ZBXD"))

	buf := new(bytes.Buffer)
	err := binary.Write(buf, binary.LittleEndian, &decodedPacket.version)
	if err != nil {
		err = fmt.Errorf("binary.Write failed for result_code: %v", err)
		return encodedPacket, err
	}
	copy(encodedPacket[4:5], buf.Bytes())

	var dataLength = int64(len(decodedPacket.data))
	buf = new(bytes.Buffer)
	err = binary.Write(buf, binary.LittleEndian, &dataLength)
	if err != nil {
		err = fmt.Errorf("binary.Write failed for data_length: %v", err)
		return encodedPacket, err
	}
	copy(encodedPacket[5:13], buf.Bytes())

	copy(encodedPacket[13:13+len(decodedPacket.data)], []byte(decodedPacket.data))
	return encodedPacket, nil
}

//Run starts a connection with a zabbix server
func Run(ctx context.Context, port string, cb callback, useTLS bool) {
	tcpAdress, err := net.ResolveTCPAddr("tcp4", port)
	if err != nil {
		log.Println(err)
		return
	}
	l, err := net.ListenTCP("tcp4", tcpAdress)

	if err != nil {
		log.Println(err)
		return
	}
	defer l.Close()
	lWrap := net.Listener(l)
	if useTLS {
	}

	var wg sync.WaitGroup
	for {
		err := l.SetDeadline(time.Now().Add(time.Second))
		if err != nil {
			log.Printf("Nrpe: setDeadline on listener failed: %v", err)
			break
		}
		c, err := lWrap.Accept()
		if ctx.Err() != nil {
			break
		}
		if errNet, ok := err.(net.Error); ok && errNet.Timeout() {
			continue
		}
		if err != nil {
			log.Printf("Nrpe accept failed: %v", err)
			break
		}

		err = c.SetDeadline(time.Now().Add(time.Second * 10))
		if err != nil {
			log.Printf("Nrpe: setDeadline on connection failed: %v", err)
			break
		}

		wg.Add(1)
		go func() {
			defer wg.Done()
			handleConnection(c, cb)
		}()
	}
	wg.Wait()
}
