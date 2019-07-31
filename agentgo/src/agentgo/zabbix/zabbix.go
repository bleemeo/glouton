package zabbix

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"log"
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
	var header int32
	buf := bytes.NewReader(packetHead)
	err = binary.Read(buf, binary.BigEndian, &header)
	if err != nil {
		err = fmt.Errorf("binary.Read failed for packet_version: %v", err)
		return decodedPacket, err
	}
	if header != int32(1514297412) { //1514297412= 0x5a, 0x42, 0x58, 0x44
		err = fmt.Errorf("wrong packet header")
		return decodedPacket, err
	}
	err = binary.Read(buf, binary.BigEndian, &decodedPacket.version)
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
	err := binary.Write(buf, binary.BigEndian, &decodedPacket.version)
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
func Run() {
	return
}
