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

package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

//nolint:gochecknoglobals
var (
	// flags related to the installation
	postInstall       = flag.Bool("post-install", false, "Run the post-install step")
	basedir           = flag.String("basedir", "", "Base directory of configuration, state file and logs")
	configFileSubpath = flag.String("config-file-subpath", "", "Path of the config file used to store the account id and registration key, relative to basedir")
	account           = flag.String("account-id", "", "Account ID")
	registration      = flag.String("registration-key", "", "Registration key of your account")
)

func fixOsPermissions() error {
	curProcess := windows.CurrentProcess()

	// we need *moar* permissions to change the owner of the files: the SE_TAKE_OWNERSHIP_NAME !
	var token windows.Token

	err := windows.OpenProcessToken(curProcess, windows.TOKEN_ADJUST_PRIVILEGES|windows.TOKEN_QUERY, &token)
	if err != nil {
		return err
	}

	// Enable the SE_TAKE_OWNERSHIP_NAME privilege.
	ownershipPrivilege, err := windows.UTF16PtrFromString("SeTakeOwnershipPrivilege")
	if err != nil {
		return err
	}

	var luid windows.LUID

	err = windows.LookupPrivilegeValue(nil, ownershipPrivilege, &luid)
	if err != nil {
		return err
	}

	privilege := windows.Tokenprivileges{PrivilegeCount: 1}
	privilege.Privileges[0].Attributes = windows.SE_PRIVILEGE_ENABLED
	privilege.Privileges[0].Luid = luid

	err = windows.AdjustTokenPrivileges(token, false, &privilege, uint32(unsafe.Sizeof(privilege)), nil, nil)
	if err != nil {
		return err
	}

	// Enable the SE_RESTORE_NAME privilege.
	ownershipPrivilege, err = windows.UTF16PtrFromString("SeRestorePrivilege")
	if err != nil {
		return err
	}

	err = windows.LookupPrivilegeValue(nil, ownershipPrivilege, &luid)
	if err != nil {
		return err
	}

	privilege.Privileges[0].Luid = luid

	err = windows.AdjustTokenPrivileges(token, false, &privilege, uint32(unsafe.Sizeof(privilege)), nil, nil)
	if err != nil {
		return err
	}

	// fix the permissions of the state.json and the logs with the SID of "NT AUTHORITY\LocalService"
	sid, _, _, err := windows.LookupSID("", `NT AUTHORITY\LocalService`)
	if err != nil {
		fmt.Printf("Couldn't retrieve the SID for LocalService: %v\n", err)
		os.Exit(1)
	}

	// TODO: read the configuration to get agent.state_file instead of a static path
	_ = updateOwner(filepath.Join(*basedir, "state.json"), sid)
	_ = updateOwner(filepath.Join(*basedir, "state.json.tmp"), sid)
	_ = updateOwner(filepath.Join(*basedir, `logs\glouton.log`), sid)

	return nil
}

func updateOwner(filepath string, sid *windows.SID) error {
	access := []windows.EXPLICIT_ACCESS{
		{
			AccessPermissions: windows.STANDARD_RIGHTS_ALL,
			AccessMode:        windows.GRANT_ACCESS,
			Inheritance:       windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
			Trustee: windows.TRUSTEE{
				MultipleTrustee:          nil,
				MultipleTrusteeOperation: windows.NO_MULTIPLE_TRUSTEE,
				TrusteeForm:              windows.TRUSTEE_IS_SID,
				TrusteeType:              windows.TRUSTEE_IS_USER,
				TrusteeValue:             windows.TrusteeValue(unsafe.Pointer(sid)),
			},
		},
	}

	acl, err := windows.ACLFromEntries(access, nil)
	if err != nil {
		return err
	}

	err = windows.SetNamedSecurityInfo(filepath, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION, sid, nil, nil, nil)
	if err != nil {
		return err
	}

	return windows.SetNamedSecurityInfo(filepath, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
}

func OSDependentMain() {
	if !*postInstall {
		return
	}

	defer os.Exit(0)

	if err := fixOsPermissions(); err != nil {
		fmt.Printf("Couldn't fix file permissions: %v\n", err)
	}

	if *configFileSubpath == "" || *basedir == "" {
		fmt.Println("No config file specified, cannot install the agent configuration")
		return
	}

	configFilePath := filepath.Join(*basedir, *configFileSubpath)

	_, err := os.Stat(configFilePath)
	if err == nil {
		fmt.Println("The config file already exists, doing nothing")
		return
	}

	fd, err := os.Create(configFilePath)
	if err != nil {
		fmt.Printf("Couldn't open the config file: %v.\n", err)
		return
	}

	fmt.Fprintf(fd, "bleemeo:\n  account_id: %s\n  registration_key: %s", *account, *registration)

	fd.Close()
}
