package nrpe

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"net"
)

type packetStruct struct {
	packetVersion int16
	packetType    int16
	crc32Value    uint32
	resultCode    int16
	alignment     int16
	bufferLength  int32
	buffer        string
}
type reducedPacket struct {
	packetType int16
	buffer     string
	err        error
}

func handleConnection(c net.Conn) {
	a := decode(c)
	if a.err != nil {
		c.Close()
		return
	}
	fmt.Printf("packet_type : %v\nbuffer : %v\n", a.packetType, a.buffer)

	var answer packetStruct
	answer.buffer = "connection successful"

	encode(c, answer)
	c.Close()
}

func decode(r io.Reader) reducedPacket {
	b := make([]byte, 16)
	r.Read(b)
	var packetVersion int16
	var bufferlength int32
	var a reducedPacket

	buf := bytes.NewReader(b[:2])
	err := binary.Read(buf, binary.BigEndian, &packetVersion)
	if err != nil {
		a.err = errors.New("binary.Read failed for packet_version")
		return a
	}
	if packetVersion == 2 {
		a.err = errors.New("invalid packet version")
		return a
	}

	buf = bytes.NewReader(b[2:4])
	err = binary.Read(buf, binary.BigEndian, &a.packetType)
	if err != nil {
		a.err = errors.New("binary.Read failed for packet_type")
		return a
	}

	buf = bytes.NewReader(b[12:16])
	err = binary.Read(buf, binary.BigEndian, &bufferlength)
	if err != nil {
		a.err = errors.New("binary.Read failed for buffer_length")
		return a
	}

	d := make([]byte, bufferlength+3)
	r.Read(d)
	i := bytes.IndexByte(d, 0x0)
	d = d[:i]
	a.buffer = string(d)
	fmt.Println(a.buffer)

	return a
}

func encode(w io.Writer, answer packetStruct) error {
	answer.packetVersion = 3
	answer.packetType = 2
	answer.bufferLength = 1017
	b2 := make([]byte, 16+answer.bufferLength+3)

	buf2 := new(bytes.Buffer)
	err := binary.Write(buf2, binary.BigEndian, &answer.packetVersion)
	if err != nil {
		fmt.Println("binary.Write failed for packet_version:", err)
		return (err)
	}
	copy(b2[:2], buf2.Bytes())

	buf2 = new(bytes.Buffer)
	err = binary.Write(buf2, binary.BigEndian, &answer.packetType)
	if err != nil {
		fmt.Println("binary.Write failed for packet_type:", err)
		return (err)
	}
	copy(b2[2:4], buf2.Bytes())

	buf2 = new(bytes.Buffer)
	err = binary.Write(buf2, binary.BigEndian, &answer.bufferLength)
	if err != nil {
		fmt.Println("binary.Write failed for buffer_length:", err)
		return (err)
	}
	copy(b2[12:16], buf2.Bytes())

	buf2 = new(bytes.Buffer)
	copy(b2[16:16+len(answer.buffer)], []byte(answer.buffer))
	answer.crc32Value = crc32.ChecksumIEEE(b2)
	buf2 = new(bytes.Buffer)
	err = binary.Write(buf2, binary.BigEndian, &answer.crc32Value)
	if err != nil {
		fmt.Println("binary.Write failed for crc32_value:", err)
		return (err)
	}
	copy(b2[4:8], buf2.Bytes())
	w.Write(b2)
	return (err)
}

func Run(PORT string) {
	l, err := net.Listen("tcp4", PORT)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer l.Close()

	for {
		c, err := l.Accept()
		if err != nil {
			fmt.Println(err)
			return
		}
		go handleConnection(c)
	}
}
