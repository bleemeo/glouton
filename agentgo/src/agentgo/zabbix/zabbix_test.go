package zabbix

import (
	"bytes"
	"io"
	"testing"
)

var versionRequest = []byte{0x5a, 0x42, 0x58, 0x44,
	0x01, 0x0d, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	0x61, 0x67, 0x65, 0x6e, 0x74, 0x2e, 0x76, 0x65, 0x72, 0x73, 0x69, 0x6f, 0x6e}
var pingRequest = []byte{0x5a, 0x42, 0x58, 0x44,
	0x01, 0x0a, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	0x61, 0x67, 0x65, 0x6e, 0x74, 0x2e, 0x70, 0x69, 0x6e, 0x67}
var pingAnswer = []byte{0x5a, 0x42, 0x58, 0x44,
	0x01, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x31}
var versionAnswer = []byte{0x5a, 0x42, 0x58, 0x44,
	0x01, 0x05, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	0x34, 0x2e, 0x32, 0x2e, 0x34}

func TestDecode(t *testing.T) {
	cases := []struct {
		in   io.Reader
		want packetStruct
	}{
		{bytes.NewReader(versionRequest), packetStruct{1, "agent.version"}},
		{bytes.NewReader(pingRequest), packetStruct{1, "agent.ping"}},
	}
	for _, c := range cases {
		got, err := decode(c.in)
		if got != c.want {
			t.Errorf("decode(zabbixPacket) == %v, want %v", got, c.want)
		}
		if err != nil {
			t.Error(err)
		}
	}
}

func TestEncode(t *testing.T) {
	cases := []struct {
		in   packetStruct
		want []byte
	}{
		{packetStruct{1, "1"}, pingAnswer},
	}
	for _, c := range cases {
		got, err := encodev1(c.in)
		if len(got) != len(c.want) {
			t.Errorf("encodeV2(%v) == %v, want %v", c.in, got, c.want)
			break
		}
		for i := 0; i < len(got); i++ {
			if got[i] != c.want[i] {
				t.Errorf("encodeV2(%v) == %v, want %v", c.in, got, c.want)
				break
			}
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

func responsewarningv3(key string, args []string) string {
	answer := "1"
	return answer
}

func TestHandleConnection(t *testing.T) {
	cases := []struct {
		in   ReaderWriter
		want []byte
	}{
		{ReaderWriter{bytes.NewReader(pingRequest), new(bytes.Buffer)}, pingAnswer},
	}
	for _, c := range cases {
		handleConnection(c.in, responsewarningv3)
		got := c.in.writer.Bytes()
		if len(got) != len(c.want) {
			t.Errorf("handleConnection(%v,response) writes %v, want %v", c.in, got, c.want)
			break
		}
		for i := 0; i < len(got); i++ {
			if got[i] != c.want[i] {
				t.Errorf("handleConnection(%v,response) writes %v, want %v", c.in, got, c.want)
				break
			}
		}
	}
}
