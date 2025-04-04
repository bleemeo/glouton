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
			t.Log(err, len(gotArgs))
		}
	}
}

func TestValidSplitData(t *testing.T) {
	cases := []struct {
		in   string
		want error
	}{
		{`key[["a",]`, ErrUnmatchedOpeningBracket},
		{`key[[a"]"]`, ErrUnmatchedOpeningBracket},
		{`key[["a","\"b\"]"]`, ErrUnmatchedOpeningBracket},
		{`key["a",["b","c\"]"]]]`, ErrUnmatchedClosingBracket},
		{"key[a ]]", ErrClosingBracketNotAllowed},
		{"key[ a]]", ErrClosingBracketNotAllowed},
		{"key[abca]654", ErrMissingClosingBracket},
		{"{}key", ErrIllegalBraces},
		{"ssh,21", ErrCommaButNotArgs},
		{"key[][]", ErrClosingBracketNotAllowed},
		{`key["a",b,["c","d\",]"]]["d"]`, ErrUnmatchedClosingBracket},
		{"key[[[]]]", ErrMultiArrayNotAllowed},
		{`key["a",["b",["c","d"],e],"f"]`, ErrMultiArrayNotAllowed},
		{`key["a","b",[["c","d\",]"]]]`, ErrMultiArrayNotAllowed},
		{`key[a]]`, ErrClosingBracketNotAllowed},
		{`key[a[b]]`, ErrClosingBracketNotAllowed},
		{`key["a",b[c,d],e]`, ErrClosingBracketNotAllowed},
		{`key["a"b]`, ErrQuotedParamContainsUnquoted},
		{`key["a",["b","]"c]]`, ErrQuotedParamContainsUnquoted},
		{`key[["]"a]]`, ErrQuotedParamContainsUnquoted},
		{`key[[a]"b"]`, ErrUnmatchedOpeningBracket},
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
			func(_ string, _ []string) (string, error) {
				return c.ReplyString, c.ReplyError
			},
		)

		got := socket.writer.Bytes()
		if !bytes.Equal(got, c.ReplyRaw) {
			t.Errorf("handleConnection([case %s]) writes %v, want %v", c.Description, got, c.ReplyRaw)

			break
		}
	}
}
