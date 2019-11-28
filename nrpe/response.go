// Copyright 2015-2019 Bleemeo
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
	"context"
	"fmt"
)

// Response return the response of an NRPE request
func (s Server) Response(ctx context.Context, request string) (string, int16, error) {
	nameContainer, ok := s.customCheck[request]
	if ok == false {
		return "", 0, fmt.Errorf("NRPE: Command '%s' not defined", request)
	}

	checkNow, err := s.discovery.GetCheckNow(nameContainer)
	if err != nil {
		return "", 0, fmt.Errorf("NRPE: Command '%s' exists but hasn't an associated check", request)
	}

	statusDescription := checkNow(ctx)
	return statusDescription.StatusDescription, int16(statusDescription.CurrentStatus.NagiosCode()), nil
}
