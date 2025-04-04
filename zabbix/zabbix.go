// Copyright 2015-2025 Bleemeo
//
// bleemeo.com an infrastructure monitoring solution in the Cloud
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package zabbix

import (
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

	"github.com/bleemeo/glouton/crashreport"
	"github.com/bleemeo/glouton/logger"
)

var (
	ErrUnmatchedOpeningBracket     = errors.New("unmatched opening bracket")
	ErrUnmatchedClosingBracket     = errors.New("unmatched closing bracket")
	ErrClosingBracketNotAllowed    = errors.New("character ] is not allowed in unquoted parameter string")
	ErrMissingClosingBracket       = errors.New("missing closing bracket at the end")
	ErrIllegalBraces               = errors.New("illegal braces")
	ErrCommaButNotArgs             = errors.New("comma but no arguments detected")
	ErrMultiArrayNotAllowed        = errors.New("multi-level arrays are not allowed")
	ErrQuotedParamContainsUnquoted = errors.New("quoted parameter cannot contain unquoted part")
	errWrongHeader                 = errors.New("wrong packet header")
)

// Server is a Zabbix server than use Callback for reply to queries.
type Server struct {
	callback    callback
	bindAddress string
}

// New returns a Zabbix server
// callback is the function responsible to generate the response for a given query.
func New(bindAddress string, callback callback) Server {
	return Server{
		callback:    callback,
		bindAddress: bindAddress,
	}
}

type packetStruct struct {
	version int8
	key     string
	args    []string
}

type callback func(key string, args []string) (string, error)

func handleConnection(c io.ReadWriteCloser, cb callback) {
	decodedRequest, err := decode(c)
	if err != nil {
		logger.V(1).Printf("Unable to decode Zabbix packet: %v", err)

		_ = c.Close()

		return
	}

	answer, err := cb(decodedRequest.key, decodedRequest.args)

	var encodedAnswer []byte

	encodedAnswer, err = encodeReply(answer, err)
	if err != nil {
		logger.V(1).Printf("Failed to encode Zabbix packet: %v", err)

		_ = c.Close()

		return
	}

	_, err = c.Write(encodedAnswer)
	if err != nil {
		logger.V(1).Printf("Failed to write Zabbix packet: %v", err)
	}

	_ = c.Close()
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
		err = errWrongHeader

		return decodedPacket, err
	}

	err = binary.Read(buf, binary.LittleEndian, &decodedPacket.version)
	if err != nil {
		err = fmt.Errorf("binary.Read failed for packet_version: %w", err)

		return decodedPacket, err
	}

	var dataLength int64

	err = binary.Read(buf, binary.LittleEndian, &dataLength)
	if err != nil {
		err = fmt.Errorf("binary.Read failed for packet_version: %w", err)

		return decodedPacket, err
	}

	packetData := make([]byte, dataLength)

	_, err = r.Read(packetData)
	if err != nil {
		err = fmt.Errorf("r.Read failed for data: %w", err)

		return decodedPacket, err
	}

	strPacketData := string(packetData)
	decodedPacket.key, decodedPacket.args, err = splitData(strPacketData)

	return decodedPacket, err
}

func splitData(request string) (string, []string, error) {
	var args []string

	if strings.Contains(request, "{") || strings.Contains(request, "}") {
		return request, args, ErrIllegalBraces
	}

	i := strings.Index(request, "[")
	if i == -1 {
		if strings.Contains(request, ",") {
			return request, args, ErrCommaButNotArgs
		}

		return request, args, nil
	}

	newrequest := strings.ReplaceAll(request, " ", "")
	key := newrequest[0:i]

	if string(newrequest[len(newrequest)-1]) != "]" {
		return key, args, ErrMissingClosingBracket
	}

	joinArgs := newrequest[i+1 : len(newrequest)-1]
	if len(joinArgs) == 0 {
		return key, []string{""}, nil
	}

	var (
		j          int
		inBrackets bool
	)

	for k, s := range joinArgs {
		if inBrackets {
			if string(s) == "[" {
				if joinArgs[k-1:k+2] != `"["` {
					return key, args, ErrMultiArrayNotAllowed
				}
			}

			if string(s) == "]" {
				if k == len(joinArgs)-1 {
					inBrackets = false

					if strings.Contains(joinArgs[j:k], `"`) {
						if strings.LastIndex(joinArgs[j:k], `"`) != k-j-1 {
							return key, args, ErrQuotedParamContainsUnquoted
						}
					}

					args = append(args, joinArgs[j:k])
					j = k + 1

					continue
				}

				if string(joinArgs[k+1]) == "]" {
					return key, args, ErrUnmatchedClosingBracket
				}
			}

			if joinArgs[k-1:k+1] == "]," {
				args = append(args, joinArgs[j:k-1])

				inBrackets = false
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
						return key, args, ErrQuotedParamContainsUnquoted
					}

					if string(joinArgs[j]) == `"` {
						args = append(args, strings.ReplaceAll(joinArgs[j+1:k-1], `\`, ""))
						j = k + 1

						continue
					}
				}

				args = append(args, joinArgs[j:k])
				j = k + 1
			}

			if string(s) == "]" {
				return key, args, ErrClosingBracketNotAllowed
			}
		}
	}

	if inBrackets {
		err := ErrUnmatchedOpeningBracket

		return key, args, err
	}

	if j == len(joinArgs) {
		if string(joinArgs[len(joinArgs)-1]) == "," {
			args = append(args, "")
		}
	} else {
		if strings.Contains(joinArgs[j:], `"`) {
			if strings.LastIndex(joinArgs, `"`) != len(joinArgs)-1 {
				return key, args, ErrQuotedParamContainsUnquoted
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

func encodeReply(message string, inputError error) ([]byte, error) {
	if inputError != nil {
		message = fmt.Sprintf("ZBX_NOTSUPPORTED\x00%s.", inputError)
	}

	dataLength := int64(len(message))

	encodedPacket := make([]byte, 13+dataLength)

	copy(encodedPacket[0:4], "ZBXD")

	encodedPacket[4] = 1 // version

	buf := new(bytes.Buffer)

	err := binary.Write(buf, binary.LittleEndian, &dataLength)
	if err != nil {
		err = fmt.Errorf("binary.Write failed for data_length: %w", err)

		return encodedPacket, err
	}

	copy(encodedPacket[5:13], buf.Bytes())
	copy(encodedPacket[13:], message)

	return encodedPacket, nil
}

// Run starts a connection with a zabbix server.
func (s Server) Run(ctx context.Context) error {
	tcpAdress, err := net.ResolveTCPAddr("tcp", s.bindAddress)
	if err != nil {
		return err
	}

	l, err := net.ListenTCP("tcp", tcpAdress)
	if err != nil {
		return err
	}

	defer l.Close()

	lWrap := net.Listener(l)

	logger.V(1).Printf("Zabbix server listening on %s", s.bindAddress)

	var wg sync.WaitGroup

	for {
		err = l.SetDeadline(time.Now().Add(time.Second))
		if err != nil {
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

			continue
		}

		err = c.SetDeadline(time.Now().Add(time.Second * 10))
		if err != nil {
			logger.V(1).Printf("Zabbix: setDeadline on connection failed: %v", err)

			continue
		}

		wg.Add(1)

		go func() {
			defer crashreport.ProcessPanic()
			defer wg.Done()

			handleConnection(c, s.callback)
		}()
	}

	wg.Wait()

	return err
}
