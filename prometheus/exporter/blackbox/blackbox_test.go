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

package blackbox

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"maps"
	"math"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/bleemeo/glouton/types"
	"github.com/google/go-cmp/cmp"
)

const (
	testTargetNotYetKnown = "this-label-value-will-be-replaced"
	testAgentFQDN         = "example.com"
	testMonitorID         = "7331d6c1-ede1-4483-a3b3-c99f0965f64b"
	testAgentID           = "1d6a2c82-4579-4f7d-91fe-3d4946aacaf7"
)

type testTarget interface {
	Start()
	Close()
	URL() string
	RootCACertificates() []*x509.Certificate
	RequestContext(ctx context.Context) context.Context
}

type testCase struct {
	name string
	// wantPoints is a subset of result points, that is extra points in result don't result in error.
	// Use absentPoints to check for absence of points.
	// In wantPoints & absentPoints, the value of "instance" label will be replaced by target URL.
	// In wantPoints, value NaN will be remplaced with value from result point. Use NaN when you don't
	// care about value. However NaN will NOT be replaced with the value 0.
	wantPoints   []types.MetricPoint
	absentPoints []map[string]string
	target       testTarget
	// Check that probe duration is between given value.
	// If the min or max value is 0, no check is done.
	probeDurationMinValue float64
	probeDurationMaxValue float64
}

// runTestCase is able to run test on http:// and tcp:// (and their SSL equivalent).
func runTestCase(t *testing.T, test testCase, usePlainTCPOrSSL bool, monitorID string, agentID string, t0 time.Time) {
	t.Helper()
	t.Parallel()

	test.target.Start()
	defer test.target.Close()

	targetURL := test.target.URL()
	if usePlainTCPOrSSL {
		targetURL = strings.Replace(targetURL, "https://", "ssl://", 1)
		targetURL = strings.Replace(targetURL, "http://", "tcp://", 1)
	}

	monitor := types.Monitor{
		ID:             monitorID,
		BleemeoAgentID: agentID,
		URL:            targetURL,
	}

	// To avoid conflicts between tests that use the same test case (HTTPS and SSL), we need
	// to make a copy of the absentPoints map and the wantPoints labels map before modifying them.
	absentPoints := make([]map[string]string, len(test.absentPoints))
	copy(absentPoints, test.absentPoints)

	for _, lbls := range absentPoints {
		if _, ok := lbls[types.LabelInstance]; ok {
			lbls[types.LabelInstance] = targetURL
		}
	}

	wantPoints := make([]types.MetricPoint, 0, len(test.wantPoints))
	for _, point := range test.wantPoints {
		lbls := make(map[string]string, len(point.Labels))
		maps.Copy(lbls, point.Labels)

		lbls[types.LabelInstance] = targetURL

		newPoint := types.MetricPoint{
			Point:       point.Point,
			Labels:      lbls,
			Annotations: point.Annotations,
		}

		wantPoints = append(wantPoints, newPoint)
	}

	ctx := t.Context()

	resPoints, err := InternalRunProbe(test.target.RequestContext(ctx), monitor, t0, test.target.RootCACertificates(), errorResolverSentinel)
	if err != nil {
		t.Fatal(err)
	}

	gotMap := make(map[string]int, len(resPoints))
	for i, got := range resPoints {
		gotMap[types.LabelsToText(got.Labels)] = i
	}

	for _, lbls := range absentPoints {
		if _, ok := gotMap[types.LabelsToText(lbls)]; ok {
			t.Errorf("got labels %v, expected not present", lbls)
		}
	}

	for _, want := range wantPoints {
		idx, ok := gotMap[types.LabelsToText(want.Labels)]
		if !ok {
			t.Errorf("got no labels %v, expected present", want.Labels)

			for _, got := range resPoints {
				if got.Labels[types.LabelName] == want.Labels[types.LabelName] {
					t.Logf("Similar labels in resPoints include: %v", got.Labels)
				}
			}

			continue
		}

		got := resPoints[idx]
		if math.IsNaN(want.Value) && got.Value != 0 {
			want.Value = got.Value
		}

		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("points mismatch: (-want +got)\n%s", diff)
		}

		if want.Labels[types.LabelName] == "probe_duration_seconds" {
			if test.probeDurationMaxValue != 0 {
				if got.Value > test.probeDurationMaxValue {
					t.Errorf("probe_duration_seconds = %v, want <= %v", got.Value, test.probeDurationMaxValue)
				}
			}

			if test.probeDurationMinValue != 0 {
				if got.Value < test.probeDurationMinValue {
					t.Errorf("probe_duration_seconds = %v, want >= %v", got.Value, test.probeDurationMinValue)
				}
			}
		}
	}
}

type testingCerts struct {
	RootCA          *x509.Certificate
	SubCA           *x509.Certificate
	SubCAExpireSoon *x509.Certificate
	SubCAExpired    *x509.Certificate

	CertLongLivedSelfSigned           tls.Certificate
	CertLongLivedSelfSignedExpired    tls.Certificate
	CertLongLivedSelfSignedWarning    tls.Certificate
	CertLongLivedOk                   tls.Certificate
	CertLongLivedCritical             tls.Certificate
	CertLongLivedExpired              tls.Certificate
	CertShortLivedCritical            tls.Certificate
	CertShortLivedWarning             tls.Certificate
	CertSubCAExpireFar                tls.Certificate
	CertUselessExpiredIntermediary    tls.Certificate
	CertSubCAWithCAExpireSoon         tls.Certificate
	CertSubCAExpireFarOldIntermediary tls.Certificate
	CertMissingIntermediary           tls.Certificate

	CARootDuration       time.Duration
	CADuration           time.Duration
	LongLiveDuration     time.Duration
	ShortLiveDuration    time.Duration
	TSLongLivedOk        time.Time
	TSLongLivedWarning   time.Time
	TSLongLivedCritical  time.Time
	TSShortLivedWarning  time.Time
	TSShortLivedCritical time.Time
	TSLongLivedExpired   time.Time

	TSRootCA     time.Time
	TSCAOk       time.Time
	TSCACritical time.Time
	TSCAExpired  time.Time
}

// timeoutTime is a time longer any of our timeouts.
const timeoutTime = 15 * time.Second

func generateCerts(t *testing.T, t0 time.Time) (testingCerts, error) {
	t.Helper()

	var err error

	result := testingCerts{
		CARootDuration:       10 * 365 * 24 * time.Hour,
		CADuration:           5 * 365 * 24 * time.Hour,
		LongLiveDuration:     365 * 24 * time.Hour,
		ShortLiveDuration:    90 * 24 * time.Hour,
		TSLongLivedOk:        t0.Add(200 * 24 * time.Hour),
		TSLongLivedWarning:   t0.Add(17 * 24 * time.Hour),
		TSLongLivedCritical:  t0.Add(9 * 24 * time.Hour),
		TSShortLivedWarning:  t0.Add(8 * 24 * time.Hour),
		TSShortLivedCritical: t0.Add(24 * time.Hour),
		TSLongLivedExpired:   t0.Add(-24 * time.Hour),

		// Don't use exactly same expiration time for CA than for certiciate.
		// This allow to distinguish each value and ensure test really test what we expect
		TSRootCA:     t0.Add(2 * 365 * 24 * time.Hour),
		TSCAOk:       t0.Add(201 * 24 * time.Hour),
		TSCACritical: t0.Add(10 * 24 * time.Hour),
		TSCAExpired:  t0.Add(-25 * time.Hour),
	}

	rootCAPrivateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return testingCerts{}, err
	}

	CAPrivateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return testingCerts{}, err
	}

	CAExpireSoonPrivateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return testingCerts{}, err
	}

	CAExpiredPrivateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return testingCerts{}, err
	}

	serverPrivateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return testingCerts{}, err
	}

	rootCACert, err := signCA(result.TSRootCA.Add(-result.CARootDuration), result.TSRootCA, rootCAPrivateKey.PublicKey, rootCAPrivateKey, nil, "The RootCA")
	if err != nil {
		return result, err
	}

	result.RootCA, err = x509.ParseCertificate(rootCACert)
	if err != nil {
		return result, err
	}

	CACert, err := signCA(result.TSCAOk.Add(-result.CADuration), result.TSCAOk, CAPrivateKey.PublicKey, rootCAPrivateKey, result.RootCA, "SubCA")
	if err != nil {
		return result, err
	}

	// This similar an older version of the same CA as CACert (same private key)
	CACertOld, err := signCA(result.TSCACritical.Add(-result.CADuration), result.TSCACritical, CAPrivateKey.PublicKey, rootCAPrivateKey, result.RootCA, "SubCA")
	if err != nil {
		return result, err
	}

	CAExpireSoonCert, err := signCA(result.TSCACritical.Add(-result.CADuration), result.TSCACritical, CAExpireSoonPrivateKey.PublicKey, rootCAPrivateKey, result.RootCA, "SubCA expire soon")
	if err != nil {
		return result, err
	}

	CAExpiredCert, err := signCA(result.TSCAExpired.Add(-result.CADuration), result.TSCAExpired, CAExpiredPrivateKey.PublicKey, rootCAPrivateKey, result.RootCA, "SubCA expired")
	if err != nil {
		return result, err
	}

	result.SubCA, err = x509.ParseCertificate(CACert)
	if err != nil {
		return result, err
	}

	result.SubCAExpireSoon, err = x509.ParseCertificate(CAExpireSoonCert)
	if err != nil {
		return result, err
	}

	result.SubCAExpired, err = x509.ParseCertificate(CAExpiredCert)
	if err != nil {
		return result, err
	}

	certSelfSignFar, err := signCert(result.TSLongLivedOk.Add(-result.LongLiveDuration), result.TSLongLivedOk, serverPrivateKey.PublicKey, serverPrivateKey, nil, "SelfSigned valid")
	if err != nil {
		return result, err
	}

	certSelfSignWarning, err := signCert(result.TSLongLivedWarning.Add(-result.LongLiveDuration), result.TSLongLivedWarning, serverPrivateKey.PublicKey, serverPrivateKey, nil, "SelfSigned expired")
	if err != nil {
		return result, err
	}

	certSelfSignExpired, err := signCert(result.TSLongLivedExpired.Add(-result.LongLiveDuration), result.TSLongLivedExpired, serverPrivateKey.PublicKey, serverPrivateKey, nil, "SelfSigned expired")
	if err != nil {
		return result, err
	}

	certRootCAFar, err := signCert(result.TSLongLivedOk.Add(-result.LongLiveDuration), result.TSLongLivedOk, serverPrivateKey.PublicKey, rootCAPrivateKey, result.RootCA, "trusted valid")
	if err != nil {
		return result, err
	}

	certRootCASoon, err := signCert(result.TSLongLivedCritical.Add(-result.LongLiveDuration), result.TSLongLivedCritical, serverPrivateKey.PublicKey, rootCAPrivateKey, result.RootCA, "trusted expire soon")
	if err != nil {
		return result, err
	}

	certShortLivedRootCACritical, err := signCert(result.TSShortLivedCritical.Add(-result.ShortLiveDuration), result.TSShortLivedCritical, serverPrivateKey.PublicKey, rootCAPrivateKey, result.RootCA, "trusted expire soon")
	if err != nil {
		return result, err
	}

	certShortLivedRootCAWarning, err := signCert(result.TSShortLivedWarning.Add(-result.ShortLiveDuration), result.TSShortLivedWarning, serverPrivateKey.PublicKey, rootCAPrivateKey, result.RootCA, "trusted expire soon")
	if err != nil {
		return result, err
	}

	certRootCAExpired, err := signCert(result.TSLongLivedExpired.Add(-result.LongLiveDuration), result.TSLongLivedExpired, serverPrivateKey.PublicKey, rootCAPrivateKey, result.RootCA, "trusted expired")
	if err != nil {
		return result, err
	}

	certSubCAExpireFar, err := signCert(result.TSLongLivedOk.Add(-result.LongLiveDuration), result.TSLongLivedOk, serverPrivateKey.PublicKey, CAPrivateKey, result.SubCA, "trusted by sub-ca")
	if err != nil {
		return result, err
	}

	// This certificate expire far, but the intermediary CA expire soon. I believe this never exist in reality (I believe CA issue at most 1 year lifespan certiciate,
	// and only use an CA-cert that have *more* than 1 year remaining)
	certSubCAWithCAExpireSoon, err := signCert(result.TSLongLivedOk.Add(-result.LongLiveDuration), result.TSLongLivedOk, serverPrivateKey.PublicKey, CAExpireSoonPrivateKey, result.SubCAExpireSoon, "trusted by a sub-ca expiring soon")
	if err != nil {
		return result, err
	}

	result.CertLongLivedSelfSigned = BuildCertChain(t, [][]byte{certSelfSignFar}, serverPrivateKey)
	result.CertLongLivedSelfSignedExpired = BuildCertChain(t, [][]byte{certSelfSignExpired}, serverPrivateKey)
	result.CertLongLivedSelfSignedWarning = BuildCertChain(t, [][]byte{certSelfSignWarning}, serverPrivateKey)
	result.CertLongLivedOk = BuildCertChain(t, [][]byte{certRootCAFar}, serverPrivateKey)
	result.CertLongLivedCritical = BuildCertChain(t, [][]byte{certRootCASoon}, serverPrivateKey)
	result.CertLongLivedExpired = BuildCertChain(t, [][]byte{certRootCAExpired}, serverPrivateKey)
	result.CertShortLivedCritical = BuildCertChain(t, [][]byte{certShortLivedRootCACritical}, serverPrivateKey)
	result.CertShortLivedWarning = BuildCertChain(t, [][]byte{certShortLivedRootCAWarning}, serverPrivateKey)
	result.CertSubCAExpireFar = BuildCertChain(t, [][]byte{certSubCAExpireFar, CACert}, serverPrivateKey)
	result.CertMissingIntermediary = BuildCertChain(t, [][]byte{certSubCAExpireFar}, serverPrivateKey)
	result.CertUselessExpiredIntermediary = BuildCertChain(t, [][]byte{certRootCAFar, CAExpiredCert}, serverPrivateKey)
	result.CertSubCAWithCAExpireSoon = BuildCertChain(t, [][]byte{certSubCAWithCAExpireSoon, CAExpireSoonCert}, serverPrivateKey)

	// This situation might never exists in reality: here the *same* private key of a CA was used for two intermediary certificates:
	//  * one that expired soon
	//  * one that expired in longer time (updated)
	// This case is here to test whether or not it is possible to get a valid trust chain with the wrong CA cert, but I'm
	// quiet sure this could never occur with public PKI because:
	//  * On some system, it could cause bugs (confusion between the two cetificates that are both identified by the identical subject)
	//  * That means re-using a private key and not re-newing it, which feels wrong security-wise
	result.CertSubCAExpireFarOldIntermediary = BuildCertChain(t, [][]byte{certSubCAExpireFar, CACertOld}, serverPrivateKey)

	return result, nil
}

func signCert(notBefore time.Time, notAfter time.Time, publicKeyToSign rsa.PublicKey, signerPrivateKey *rsa.PrivateKey, signerCertificate *x509.Certificate, org string) ([]byte, error) {
	keyUsage := x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)

	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return nil, err
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{org},
		},
		NotBefore: notBefore,
		NotAfter:  notAfter,

		KeyUsage:              keyUsage,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
		IsCA:                  signerCertificate == nil,
	}

	if signerCertificate == nil {
		signerCertificate = &template
	}

	return x509.CreateCertificate(rand.Reader, &template, signerCertificate, &publicKeyToSign, signerPrivateKey)
}

// signCA create a certificate template and sign it using signerPrivateKey & signerCertificate.
// signerPrivateKey is required. In case of self-signed, signerCertificate should be nil and signerPrivateKey should match the public key.
func signCA(notBefore time.Time, notAfter time.Time, publicKeyToSign rsa.PublicKey, signerPrivateKey *rsa.PrivateKey, signerCertificate *x509.Certificate, org string) ([]byte, error) {
	keyUsage := x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign | x509.KeyUsageCRLSign

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)

	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return nil, err
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{org},
		},
		NotBefore: notBefore,
		NotAfter:  notAfter,

		KeyUsage:              keyUsage,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	if signerCertificate == nil {
		signerCertificate = &template
	}

	return x509.CreateCertificate(rand.Reader, &template, signerCertificate, &publicKeyToSign, signerPrivateKey)
}

func MustParseCertificate(t *testing.T, derByre []byte) *x509.Certificate {
	t.Helper()

	result, err := x509.ParseCertificate(derByre)
	if err != nil {
		panic(err)
	}

	return result
}

func BuildCertChain(t *testing.T, derList [][]byte, privateKey *rsa.PrivateKey) tls.Certificate {
	t.Helper()

	var privateKeyInterface crypto.PrivateKey

	if privateKey != nil {
		privateKeyInterface = privateKey
	}

	return tls.Certificate{
		Certificate: derList,
		Leaf:        MustParseCertificate(t, derList[0]),
		PrivateKey:  privateKeyInterface,
	}
}
