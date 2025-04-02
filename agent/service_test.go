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

package agent

import "testing"

func Test_parsePostfix(t *testing.T) {
	tests := []struct {
		name    string
		output  []byte
		wantN   float64
		wantErr bool
	}{
		{
			name:    "empty",
			output:  []byte("Mail queue is empty\n"),
			wantErr: false,
			wantN:   0,
		},
		{
			name:    "unconfigured",
			output:  []byte("postqueue: fatal: open /etc/postfix/main.cf: No such file or directory\n"),
			wantErr: true,
		},
		{
			name: "2 mails",
			output: []byte(`-Queue ID-  --Size-- ----Arrival Time---- -Sender/Recipient-------
1C92E7D564     4357 Tue Jan 28 06:58:20  root
                                         ubuntu-upgrades@example.com

36BF87D65A     1363 Wed Feb 12 06:10:02  root
                                         ubuntu-upgrades@example.com

-- 5 Kbytes in 2 Requests.
			`),
			wantN: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotN, err := parsePostfix(tt.output)
			if (err != nil) != tt.wantErr {
				t.Errorf("parsePostfix() error = %v, wantErr %v", err, tt.wantErr)

				return
			}

			if gotN != tt.wantN {
				t.Errorf("parsePostfix() = %v, want %v", gotN, tt.wantN)
			}
		})
	}
}
