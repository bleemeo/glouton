package obfuscation

import (
	"testing"
)

func TestIsSensitive(t *testing.T) {
	cases := []struct {
		key                 string
		expectedSensitivity bool
	}{
		{
			key:                 "minio_pubkey",
			expectedSensitivity: false,
		},
		{
			key:                 "minio_public_key",
			expectedSensitivity: false,
		},
		{
			key:                 "public_minio_key",
			expectedSensitivity: true,
		},
		{
			key:                 "minio_key_file",
			expectedSensitivity: false,
		},
		{
			key:                 "minio_cert_file_password",
			expectedSensitivity: true,
		},
		{
			key:                 "PASSWORD_PUBLIC_KEY",
			expectedSensitivity: true,
		},
		{
			key:                 "PUBKEY_PASSWORD",
			expectedSensitivity: true,
		},
		{
			key:                 "KEY-FOR-PUBLIC-KEY",
			expectedSensitivity: true,
		},
		{
			key:                 "pubkey_with_key_and_key_file",
			expectedSensitivity: true,
		},
		{
			key:                 "keyfile_public_key",
			expectedSensitivity: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			t.Parallel()

			sensitive := isSensitive(tc.key)
			if sensitive != tc.expectedSensitivity {
				if tc.expectedSensitivity {
					t.Fatalf("Expected %q to be sensitive", tc.key)
				} else {
					t.Fatalf("Expected %q not to be sensitive", tc.key)
				}
			}
		})
	}
}
