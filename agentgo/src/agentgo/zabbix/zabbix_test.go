package zabbix

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"reflect"
	"testing"
)

func TestDecode(t *testing.T) {
	cases := []struct {
		in   io.Reader
		want packetStruct
	}{
		{bytes.NewReader(versionRequest), packetStruct{version: 1, key: "agent.version"}},
		{bytes.NewReader(pingRequest), packetStruct{version: 1, key: "agent.ping"}},
		{bytes.NewReader(discRequest), packetStruct{version: 1, key: "net.if.discovery"}},
		{bytes.NewReader(inloRequest), packetStruct{1, "net.if.in", []string{"lo"}}},
		{bytes.NewReader(cpuUtilRequest), packetStruct{1, "system.cpu.util", []string{"all", "user", "avg1"}}},
	}
	for _, c := range cases {
		got, err := decode(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("decode(zabbixPacket) == %v, want %v", got, c.want)
		}
		if err != nil {
			t.Error(err)
		}
	}
}

func TestSplitData(t *testing.T) {
	cases := []struct {
		in       string
		wantKey  string
		wantArgs []string
	}{
		{in: "key", wantKey: "key"},
		{"key[ ]", "key", []string{""}},
		{"key[a]", "key", []string{"a"}},
		{`key[""]`, "key", []string{""}},
		{"key[ ]", "key", []string{""}},
		{`key[ ""]`, "key", []string{""}},
		{`key[ "" ]`, "key", []string{""}},
		{"key[ a]", "key", []string{"a"}},
		{"key[ a ]", "key", []string{"a"}},
		{`key["a"]`, "key", []string{"a"}},
		{`key["a",]`, "key", []string{"a", ""}},
		{"key[a,]", "key", []string{"a", ""}},
		{"key[a,b,c]", "key", []string{"a", "b", "c"}},
		{`key["a","b","c"]`, "key", []string{"a", "b", "c"}},
		{"key[a,[b,c]]", "key", []string{"a", "b,c"}},
		{"key[a,[b,]]", "key", []string{"a", "b,"}},
		{"key[a,b[c]", "key", []string{"a", "b[c"}},
		{`key["a","b",["c","d\",]"]]`, "key", []string{"a", "b", `"c","d\",]"`}},
		{`key["a","b",["c","d\",]"],[e,f]]`, "key", []string{"a", "b", `"c","d\",]"`, "e,f"}},
		{`key[a"b"]`, "key", []string{`a"b"`}},
		{`key["a",b"c",d]`, "key", []string{"a", `b"c"`, "d"}},
		{`key["\"aaa\"",b,"" , " ",ccc]`, "key", []string{`"aaa"`, "b", "", "", "ccc"}},
	}
	for _, c := range cases {
		gotKey, gotArgs, err := splitData(c.in)
		if gotKey != c.wantKey || !reflect.DeepEqual(gotArgs, c.wantArgs) {
			t.Errorf("splitData(%v) == %v+%v, want %v+%v", c.in, gotKey, gotArgs, c.wantKey, c.wantArgs)
			fmt.Println(err, len(gotArgs))
		}
	}
}

func TestValidSplitData(t *testing.T) {
	cases := []struct {
		in   string
		want error
	}{
		{`key[["a",]`, errors.New("unmatched opening bracket")},
		{`key[[a"]"]`, errors.New("unmatched opening bracket")},
		{`key[["a","\"b\"]"]`, errors.New("unmatched opening bracket")},
		{`key["a",["b","c\"]"]]]`, errors.New("unmatched closing bracket")},
		{"key[a ]]", errors.New("character ] is not allowed in unquoted parameter string")},
		{"key[ a]]", errors.New("character ] is not allowed in unquoted parameter string")},
		{"key[abca]654", errors.New("missing closing bracket at the end")},
		{"{}key", errors.New("illegal braces")},
		{"ssh,21", errors.New("comma but no arguments detected")},
		{"key[][]", errors.New("character ] is not allowed in unquoted parameter string")},
		{`key["a",b,["c","d\",]"]]["d"]`, errors.New("unmatched closing bracket")},
		{"key[[[]]]", errors.New("multi-level arrays are not allowed")},
		{`key["a",["b",["c","d"],e],"f"]`, errors.New("multi-level arrays are not allowed")},
		{`key["a","b",[["c","d\",]"]]]`, errors.New("multi-level arrays are not allowed")},
		{`key[a]]`, errors.New("character ] is not allowed in unquoted parameter string")},
		{`key[a[b]]`, errors.New("character ] is not allowed in unquoted parameter string")},
		{`key["a",b[c,d],e]`, errors.New("character ] is not allowed in unquoted parameter string")},
		{`key["a"b]`, errors.New("quoted parameter cannot contain unquoted part")},
		{`key["a",["b","]"c]]`, errors.New("quoted parameter cannot contain unquoted part")},
		{`key[["]"a]]`, errors.New("quoted parameter cannot contain unquoted part")},
		{`key[[a]"b"]`, errors.New("unmatched opening bracket")},
	}
	for _, c := range cases {
		_, _, err := splitData(c.in)
		if !reflect.DeepEqual(err, c.want) {
			t.Errorf("splitData(%v) returns %v, want %v", c.in, err, c.want)
		}
	}
}

func TestEncode(t *testing.T) {
	cases := []struct {
		in   packetStruct
		want []byte
	}{
		{packetStruct{version: 1, key: "1"}, pingAnswer},
		{packetStruct{version: 1, key: "4.2.4"}, versionAnswer},
		{packetStruct{version: 1, key: discString}, discAnswer},
		{packetStruct{version: 1, key: "797826"}, inloAnswer},
		{packetStruct{version: 1, key: "3.008123"}, cpuUtilAnswer},
	}
	for _, c := range cases {
		got, err := encodev1(c.in)
		if !bytes.Equal(got, c.want) {
			t.Errorf("encodeV2(%v) == %v, want %v", c.in, got, c.want)
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

func responsev1(key string, args []string) string {
	if key == "agent.ping" {
		return "1"
	}
	if key == "agent.version" {
		return "4.2.4"
	}
	return ""
}

func TestHandleConnection(t *testing.T) {
	cases := []struct {
		in   ReaderWriter
		want []byte
	}{
		{ReaderWriter{bytes.NewReader(pingRequest), new(bytes.Buffer)}, pingAnswer},
		{ReaderWriter{bytes.NewReader(versionRequest), new(bytes.Buffer)}, versionAnswer},
	}
	for _, c := range cases {
		handleConnection(c.in, responsev1)
		got := c.in.writer.Bytes()
		if !bytes.Equal(got, c.want) {
			t.Errorf("handleConnection(%v,response) writes %v, want %v", c.in, got, c.want)
			break
		}

	}
}
