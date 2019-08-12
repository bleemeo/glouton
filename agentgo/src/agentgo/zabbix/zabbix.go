package zabbix

import (
	"agentgo/logger"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"
)

type packetStruct struct {
	version int8
	key     string
	args    []string
}

type callback func(key string, args []string) string

func handleConnection(c io.ReadWriteCloser, cb callback) {
	decodedRequest, err := decode(c)
	if err != nil {
		logger.V(1).Printf("Unable to decode Zabbix packet: %v", err)
		c.Close()
		return
	}

	var answer packetStruct
	answer.key = cb(decodedRequest.key, decodedRequest.args)
	answer.version = decodedRequest.version

	var encodedAnswer []byte
	if answer.version == 1 {
		encodedAnswer, err = encodev1(answer)
	}
	if err != nil {
		logger.V(1).Println(err)
		c.Close()
		return
	}

	_, err = c.Write(encodedAnswer)
	if err != nil {
		logger.V(1).Printf("Answer writing failed: %v", err)
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

	if !bytes.Equal(header, []byte("ZBXD")) {
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
	strPacketData := string(packetData)
	decodedPacket.key, decodedPacket.args, err = splitData(strPacketData)

	return decodedPacket, err
}

func splitData(request string) (string, []string, error) {
	var args []string
	if strings.Contains(request, "{") || strings.Contains(request, "}") {
		return request, args, errors.New("illegal braces")
	}
	i := strings.Index(request, "[")
	if i == -1 {
		if strings.Contains(request, ",") {
			return request, args, errors.New("comma but no arguments detected")
		}
		return request, args, nil
	}
	newrequest := strings.Replace(request, " ", "", -1)
	key := newrequest[0:i]
	if string(newrequest[len(newrequest)-1]) != "]" {
		return key, args, errors.New("missing closing bracket at the end")
	}
	joinArgs := newrequest[i+1 : len(newrequest)-1]
	if len(joinArgs) == 0 {
		return key, []string{""}, nil
	}
	var j int
	var inBrackets bool
	for k, s := range joinArgs {
		if inBrackets {
			if string(s) == "[" {
				if joinArgs[k-1:k+2] != `"["` {
					return key, args, errors.New("multi-level arrays are not allowed")
				}
			}

			if string(s) == "]" {
				if k == len(joinArgs)-1 {
					inBrackets = false
					if strings.Contains(joinArgs[j:k], `"`) {
						if strings.LastIndex(joinArgs[j:k], `"`) != k-j-1 {
							return key, args, errors.New("quoted parameter cannot contain unquoted part")
						}
					}
					args = append(args, joinArgs[j:k])
					j = k + 1
					continue
				}
				if string(joinArgs[k+1]) == "]" {
					return key, args, errors.New("unmatched closing bracket")
				}
			}
			if joinArgs[k-1:k+1] == "]," {
				inBrackets = false
				args = append(args, joinArgs[j:k-1])
				j = k + 1
			}
		} else {
			if string(s) == "[" && j == k {
				inBrackets = true
				j = k + 1
			}
			if string(s) == "," {
				if strings.Contains(joinArgs[j:k], `"`) {
					if strings.LastIndex(joinArgs[j:k], `"`) != k-j-1 {
						return key, args, errors.New("quoted parameter cannot contain unquoted part")
					}
					if string(joinArgs[j]) == `"` {
						args = append(args, strings.Replace(joinArgs[j+1:k-1], `\`, "", -1))
						j = k + 1
						continue
					}
				}
				args = append(args, joinArgs[j:k])
				j = k + 1
			}
			if string(s) == "]" {
				return key, args, errors.New("character ] is not allowed in unquoted parameter string")
			}
		}
	}
	if inBrackets {
		err := errors.New("unmatched opening bracket")
		return key, args, err
	}
	if j == len(joinArgs) {
		if string(joinArgs[len(joinArgs)-1]) == "," {
			args = append(args, "")
		}
	} else {
		if strings.Contains(joinArgs[j:], `"`) {
			if strings.LastIndex(joinArgs, `"`) != len(joinArgs)-1 {
				return key, args, errors.New("quoted parameter cannot contain unquoted part")
			}
			if string(joinArgs[j]) == `"` {
				args = append(args, joinArgs[j+1:len(joinArgs)-1])
				return key, args, nil
			}
		}
		args = append(args, joinArgs[j:])
	}
	return key, args, nil
}

func encodev1(decodedPacket packetStruct) ([]byte, error) {
	var dataLength = int64(len(decodedPacket.key))
	encodedPacket := make([]byte, 13+dataLength)

	copy(encodedPacket[0:4], []byte("ZBXD"))

	buf := new(bytes.Buffer)
	err := binary.Write(buf, binary.LittleEndian, &decodedPacket.version)
	if err != nil {
		err = fmt.Errorf("binary.Write failed for result_code: %v", err)
		return encodedPacket, err
	}
	copy(encodedPacket[4:5], buf.Bytes())

	buf = new(bytes.Buffer)
	err = binary.Write(buf, binary.LittleEndian, &dataLength)
	if err != nil {
		err = fmt.Errorf("binary.Write failed for data_length: %v", err)
		return encodedPacket, err
	}
	copy(encodedPacket[5:13], buf.Bytes())

	copy(encodedPacket[13:], []byte(decodedPacket.key))
	return encodedPacket, nil
}

//Run starts a connection with a zabbix server
func Run(ctx context.Context, port string, cb callback, useTLS bool) {
	tcpAdress, err := net.ResolveTCPAddr("tcp4", port)
	if err != nil {
		logger.V(1).Println(err)
		return
	}
	l, err := net.ListenTCP("tcp4", tcpAdress)

	if err != nil {
		logger.V(1).Println(err)
		return
	}
	defer l.Close()
	lWrap := net.Listener(l)
	if useTLS {
		logger.V(1).Println("useTLS")
	}

	var wg sync.WaitGroup
	for {
		err := l.SetDeadline(time.Now().Add(time.Second))
		if err != nil {
			logger.V(1).Printf("Zabbix: setDeadline on listener failed: %v", err)
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
			logger.V(1).Printf("Zabbix accept failed: %v", err)
			break
		}

		err = c.SetDeadline(time.Now().Add(time.Second * 10))
		if err != nil {
			logger.V(1).Printf("Zabbix: setDeadline on connection failed: %v", err)
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
