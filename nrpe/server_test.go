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

package nrpe

import (
	"bytes"
	"context"
	"io"
	"testing"
)

type packetCapture struct {
	Description string
	QueryString string
	QueryRaw    []byte
	ReplyRaw    []byte
	ReplyString string
	ReplyError  error
	Version     int16
	ReplyCode   int16
}

func TestDecode(t *testing.T) {
	for _, c := range allPackets {
		got, err := decode(bytes.NewReader(c.QueryRaw))
		want := reducedPacket{
			packetVersion: c.Version,
			packetType:    1, // query
			buffer:        c.QueryString,
		}

		if got != want {
			t.Errorf("decode([data of case %s) == %v, want %v", c.Description, got, want)
		}

		if err != nil {
			t.Error(err)
		}
	}

	for _, c := range allPackets {
		got, err := decode(bytes.NewReader(c.ReplyRaw))
		want := reducedPacket{
			packetVersion: c.Version,
			packetType:    2, // reply
			resultCode:    c.ReplyCode,
			buffer:        c.ReplyString,
		}

		if c.ReplyError != nil {
			want.resultCode = 3
			want.buffer = c.ReplyError.Error()

			if c.Version == 2 && len(want.buffer) > 1023 {
				want.buffer = want.buffer[:1023]
			}
		}

		if got != want {
			t.Errorf("decode([data of case %s) == %v, want %v", c.Description, got, want)
		}

		if err != nil {
			t.Error(err)
		}
	}
}

func TestDecodecrc32(t *testing.T) {
	for _, c := range allPackets {
		nrpePacketCopy := make([]byte, len(checkLoad.QueryRaw))
		copy(nrpePacketCopy, c.QueryRaw)

		nrpePacketCopy[18] += 42 // alter packet to break crc32

		_, err := decode(bytes.NewReader(nrpePacketCopy))
		if err == nil {
			t.Errorf("No error for CRC32 check on test %s", c.Description)
		}
	}
}

func TestDecodeEncodeV3(t *testing.T) {
	cases := reducedPacket{3, 2, 0, "connection successful"}
	inter, _ := encodeV3(cases)

	got, err := decode(bytes.NewReader(inter))
	if got != cases {
		t.Errorf("decode(encodeV3(%v)) == %v, want %v", cases, got, cases)
	}

	if err != nil {
		t.Error(err)
	}
}

func TestDecodeEncode(t *testing.T) {
	for _, c := range allPackets {
		packet := reducedPacket{
			packetVersion: c.Version,
			packetType:    2,
			resultCode:    c.ReplyCode,
			buffer:        c.ReplyString,
		}

		if c.ReplyError != nil {
			packet.resultCode = 3
			packet.buffer = c.ReplyError.Error()
		}

		var inter []byte

		switch c.Version {
		case 3:
			inter, _ = encodeV3(packet)
		case 2:
			var rndBytes [2]byte

			copy(rndBytes[:], c.ReplyRaw[len(c.ReplyRaw)-2:])

			inter, _ = encodeV2(packet, rndBytes)

			if len(packet.buffer) > 1023 {
				packet.buffer = packet.buffer[:1023]
			}
		}

		got, err := decode(bytes.NewReader(inter))
		if got != packet {
			t.Errorf("decode(encodeV3([case %s])) == %v, want %v", c.Description, got, packet)
		}

		if err != nil {
			t.Error(err)
		}
	}
}

func TestEncode(t *testing.T) {
	for _, c := range allPackets {
		inPacket := reducedPacket{
			packetVersion: c.Version,
			packetType:    2,
			resultCode:    c.ReplyCode,
			buffer:        c.ReplyString,
		}

		var (
			got []byte
			err error
		)

		if c.ReplyError != nil {
			inPacket.resultCode = 3
			inPacket.buffer = c.ReplyError.Error()
		}

		switch c.Version {
		case 2:
			var rndBytes [2]byte

			copy(rndBytes[:], c.ReplyRaw[len(c.ReplyRaw)-2:])

			got, err = encodeV2(inPacket, rndBytes)
		case 3:
			got, err = encodeV3(inPacket)
		}

		if !bytes.Equal(got, c.ReplyRaw) {
			t.Errorf("encodeV%d(%v) == %v, want %v", c.Version, inPacket, got, c.ReplyRaw)

			break
		}

		if err != nil {
			t.Error(err)
		}
	}
}

type ReaderWriter struct {
	reader io.Reader
	writer *bytes.Buffer
}

func (rw ReaderWriter) Read(b []byte) (int, error) {
	return rw.reader.Read(b)
}

func (rw ReaderWriter) Write(b []byte) (int, error) {
	return rw.writer.Write(b)
}

func (rw ReaderWriter) Close() error {
	return nil
}

func TestHandleConnection(t *testing.T) {
	for _, c := range allPackets {
		socket := ReaderWriter{
			bytes.NewReader(c.QueryRaw),
			new(bytes.Buffer),
		}

		var rndBytes [2]byte

		copy(rndBytes[:], c.ReplyRaw[len(c.ReplyRaw)-2:])
		handleConnection(
			t.Context(),
			socket,
			func(_ context.Context, _ string) (string, int16, error) {
				return c.ReplyString, c.ReplyCode, c.ReplyError
			},
			rndBytes,
		)

		got := socket.writer.Bytes()
		if !bytes.Equal(got, c.ReplyRaw) {
			t.Errorf("handleConnection([case %s]) == %v, want %v", c.Description, got, c.ReplyRaw)

			break
		}
	}
}
