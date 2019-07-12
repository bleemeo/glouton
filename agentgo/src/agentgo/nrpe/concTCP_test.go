package nrpe

import (
	"bytes"
	"io"
	"testing"
)

func TestDecode(t *testing.T) {
	cases := []struct {
		in   io.Reader
		want reducedPacket
	}{
		{bytes.NewReader(requestCheckLoad), reducedPacket{3, 1, 0, "check_load"}},
		{bytes.NewReader(requestCheckUsers), reducedPacket{3, 1, 0, "check_users"}},
		{bytes.NewReader(requestBig), reducedPacket{3, 1, 0, Answer}},
		{bytes.NewReader(answer4), reducedPacket{3, 2, 1, Answer}},
		{bytes.NewReader(requestCheckBig), reducedPacket{2, 1, 17769, "check_big"}}, //17769 result_code not interesting for requests
	}
	for _, c := range cases {
		got, err := decode(c.in)
		if got != c.want {
			t.Errorf("decode(nrpePacket) == %v, want %v", got, c.want)
		}
		if err != nil {
			t.Error(err)
		}
	}
}

func TestDecodecrc32(t *testing.T) {
	nrpePacketCopy := make([]byte, len(requestCheckLoad))
	copy(nrpePacketCopy, requestCheckLoad)
	nrpePacketCopy[6] = 0x2d //modification of a single byte
	cases := []io.Reader{
		bytes.NewReader(nrpePacketCopy),
	}
	for _, c := range cases {
		_, err := decode(c)
		if err == nil {
			t.Error("no error for crc32 value")
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

func TestEncodeV2(t *testing.T) {
	cases := []struct {
		inPacket reducedPacket
		inbyte   [2]byte
		want     []byte
	}{
		{reducedPacket{2, 2, 1, "WARNING - load average: 0.12, 0.11, 0.12|load1=0.120;0.150;0.300;0; load5=0.107;0.100;0.250;0; load15=0.125;0.050;0.200;0; "}, [2]byte{0x53, 0x51}, answerV2},
	}
	for _, c := range cases {
		got, err := encodeV2(c.inPacket, c.inbyte)
		if len(got) != len(c.want) {
			t.Errorf("encodeV2(%v) == %v, want %v", c.inPacket, got, c.want)
			break
		}
		for i := 0; i < 1034; i++ {
			if got[i] != c.want[i] {
				t.Errorf("encodeV3(%v) == %v, want %v", c.inPacket, got, c.want)
				break
			}
		}
		if err != nil {
			t.Error(err)
		}
	}
}

func TestEncodeV3(t *testing.T) {
	cases := []struct {
		in   reducedPacket
		want []byte
	}{
		{reducedPacket{3, 1, 1, "WARNING - load average: 0.02, 0.07, 0.06|load1=0.022;0.150;0.300;0; load5=0.068;0.100;0.250;0; load15=0.060;0.050;0.200;0; "}, answer1},
		{reducedPacket{3, 1, 0, "USERS OK - 1 users currently logged in |users=1;5;10;0"}, answer2},
		{reducedPacket{3, 2, 1, Answer}, answer4},
	}
	for _, c := range cases {
		got, err := encodeV3(c.in)
		if len(got) != len(c.want) {
			t.Errorf("encodeV3(%v) == %v, want %v", c.in, got, c.want)
			break
		}
		for i := 0; i < len(got); i++ {
			if got[i] != c.want[i] {
				t.Errorf("encodeV3(%v) == %v, want %v", c.in, got, c.want)
				break
			}
		}
		if err != nil {
			t.Error(err)
		}
	}
}

func response(command string) (string, int16) {
	answer := command + " ok"
	resultCode := int16(1)
	return answer, resultCode
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
func responsewarning(command string) (string, int16) {
	answer := "WARNING - load average: 0.02, 0.07, 0.06|load1=0.022;0.150;0.300;0; load5=0.068;0.100;0.250;0; load15=0.060;0.050;0.200;0; "
	resultCode := int16(1)
	return answer, resultCode
}

func TestHandleConnection(t *testing.T) {
	cases := []struct {
		in   ReaderWriter
		want []byte
	}{
		{ReaderWriter{bytes.NewReader(requestCheckLoad), new(bytes.Buffer)}, answer1},
	}
	for _, c := range cases {
		handleConnection(c.in, responsewarning)
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
