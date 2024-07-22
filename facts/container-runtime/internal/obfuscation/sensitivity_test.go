package obfuscation

import (
	"testing"
)

func TestIsSensitive(t *testing.T) {
	cases := []struct {
		key               string
		expectedSensitive bool
	}{
		{
			key:               "minio_pubkey",
			expectedSensitive: false,
		},
		{
			key:               "minio_public_key",
			expectedSensitive: false,
		},
		{
			key:               "public_minio_key",
			expectedSensitive: true,
		},
		{
			key:               "minio_key_file",
			expectedSensitive: false,
		},
		{
			key:               "minio_cert_file_password",
			expectedSensitive: true,
		},
		{
			key:               "PASSWORD_PUBLIC_KEY",
			expectedSensitive: true,
		},
		{
			key:               "PUBKEY_PASSWORD",
			expectedSensitive: true,
		},
		{
			key:               "KEY-FOR-PUBLIC-KEY",
			expectedSensitive: true,
		},
		{
			key:               "pubkey_with_key_and_key_file",
			expectedSensitive: true,
		},
		{
			key:               "keyfile_public_key",
			expectedSensitive: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			t.Parallel()

			sensitive := isSensitive(tc.key)
			if sensitive != tc.expectedSensitive {
				if tc.expectedSensitive {
					t.Fatalf("Expected %q to be sensitive", tc.key)
				} else {
					t.Fatalf("Expected %q not to be sensitive", tc.key)
				}
			}
		})
	}
}
