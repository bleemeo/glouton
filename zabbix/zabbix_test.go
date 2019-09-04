package zabbix

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"reflect"
	"testing"
)

type packetCapture struct {
	Description string
	QueryKey    string
	QueryArgs   []string
	QueryRaw    []byte
	ReplyRaw    []byte
	ReplyString string
	ReplyError  error
}

func TestDecode(t *testing.T) {
	for _, c := range allPackets {
		got, err := decode(bytes.NewReader(c.QueryRaw))
		want := packetStruct{
			version: 1,
			key:     c.QueryKey,
			args:    c.QueryArgs,
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("decode(zabbixPacket) == %v, want %v", got, want)
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
		{"key[]", "key", []string{""}},
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
	for _, c := range allPackets {
		got, err := encodeReply(c.ReplyString, c.ReplyError)
		if !bytes.Equal(got, c.ReplyRaw) {
			t.Errorf("encodev1(%#v, %#v) == %v, want %v", c.ReplyString, c.ReplyError, got, c.ReplyRaw)
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
		handleConnection(
			socket,
			func(key string, args []string) (string, error) {
				return c.ReplyString, c.ReplyError //nolint: scopelint
			},
		)
		got := socket.writer.Bytes()
		if !bytes.Equal(got, c.ReplyRaw) {
			t.Errorf("handleConnection([case %s]) writes %v, want %v", c.Description, got, c.ReplyRaw)
			break
		}

	}
}