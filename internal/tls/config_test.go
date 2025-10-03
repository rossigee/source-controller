/*
Copyright 2023 The Flux authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package tls

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/url"
	"reflect"
	"testing"

	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func Test_tlsClientConfigFromSecret(t *testing.T) {
	kubernetesTlsSecretFixture := validTlsSecret(t, true)
	tlsSecretFixture := validTlsSecret(t, false)

	tests := []struct {
		name      string
		secret    corev1.Secret
		modify    func(secret *corev1.Secret)
		tlsKeys   bool
		checkType bool
		url       string
		wantErr   bool
		wantNil   bool
	}{
		{
			name:    "tls.crt, tls.key and ca.crt",
			secret:  kubernetesTlsSecretFixture,
			modify:  nil,
			tlsKeys: true,
			url:     "https://example.com",
		},
		{
			name:    "certFile, keyFile and caFile",
			secret:  tlsSecretFixture,
			modify:  nil,
			tlsKeys: false,
			url:     "https://example.com",
		},
		{
			name:    "without tls.crt",
			secret:  kubernetesTlsSecretFixture,
			modify:  func(s *corev1.Secret) { delete(s.Data, "tls.crt") },
			tlsKeys: true,
			wantErr: true,
			wantNil: true,
		},
		{
			name:    "without tls.key",
			secret:  kubernetesTlsSecretFixture,
			modify:  func(s *corev1.Secret) { delete(s.Data, "tls.key") },
			tlsKeys: true,
			wantErr: true,
			wantNil: true,
		},
		{
			name:    "without ca.crt",
			secret:  kubernetesTlsSecretFixture,
			modify:  func(s *corev1.Secret) { delete(s.Data, "ca.crt") },
			tlsKeys: true,
		},
		{
			name:    "empty secret",
			secret:  corev1.Secret{},
			tlsKeys: true,
			wantNil: true,
		},
		{
			name:      "docker config secret with type checking enabled",
			secret:    tlsSecretFixture,
			modify:    func(secret *corev1.Secret) { secret.Type = corev1.SecretTypeDockerConfigJson },
			tlsKeys:   false,
			checkType: true,
			wantErr:   true,
			wantNil:   true,
		},
		{
			name:    "docker config secret with type checking disabled",
			secret:  tlsSecretFixture,
			modify:  func(secret *corev1.Secret) { secret.Type = corev1.SecretTypeDockerConfigJson },
			tlsKeys: false,
			url:     "https://example.com",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			secret := tt.secret.DeepCopy()
			if tt.modify != nil {
				tt.modify(secret)
			}

			tlsConfig, _, err := tlsClientConfigFromSecret(*secret, tt.url, tt.tlsKeys, tt.checkType)
			g.Expect(err != nil).To(Equal(tt.wantErr), fmt.Sprintf("expected error: %v, got: %v", tt.wantErr, err))
			g.Expect(tlsConfig == nil).To(Equal(tt.wantNil))
			if tt.url != "" {
				u, _ := url.Parse(tt.url)
				g.Expect(u.Hostname()).To(Equal(tlsConfig.ServerName))
			}
		})
	}
}

// validTlsSecret creates a secret containing key pair and CA certificate that are
// valid from a syntax (minimum requirements) perspective.
func validTlsSecret(t *testing.T, kubernetesTlsKeys bool) corev1.Secret {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal("Private key cannot be created.", err.Error())
	}

	certTemplate := x509.Certificate{
		SerialNumber: big.NewInt(1337),
	}
	cert, err := x509.CreateCertificate(rand.Reader, &certTemplate, &certTemplate, &key.PublicKey, key)
	if err != nil {
		t.Fatal("Certificate cannot be created.", err.Error())
	}

	ca := &x509.Certificate{
		SerialNumber: big.NewInt(7331),
		IsCA:         true,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
	}

	caPrivKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		t.Fatal("CA private key cannot be created.", err.Error())
	}

	caBytes, err := x509.CreateCertificate(rand.Reader, ca, ca, &caPrivKey.PublicKey, caPrivKey)
	if err != nil {
		t.Fatal("CA certificate cannot be created.", err.Error())
	}

	keyPem := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})

	certPem := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: cert,
	})

	caPem := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: caBytes,
	})

	crtKey := corev1.TLSCertKey
	pkKey := corev1.TLSPrivateKeyKey
	caKey := CACrtKey
	if !kubernetesTlsKeys {
		crtKey = "certFile"
		pkKey = "keyFile"
		caKey = "caFile"
	}
	return corev1.Secret{
		Data: map[string][]byte{
			crtKey: []byte(certPem),
			pkKey:  []byte(keyPem),
			caKey:  []byte(caPem),
		},
	}
}

func TestCAFromConfigMap(t *testing.T) {
	// Generate a valid CA certificate using the existing test helper
	testSecret := validTlsSecret(t, true)
	tlsCA := string(testSecret.Data[CACrtKey])

	tests := []struct {
		name      string
		configMap corev1.ConfigMap
		want      []byte
		wantErr   bool
	}{
		{
			name: "valid CA certificate",
			configMap: corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name: "ca-cert",
				},
				Data: map[string]string{
					"ca.crt": tlsCA,
				},
			},
			want:    []byte(tlsCA),
			wantErr: false,
		},
		{
			name: "missing ca.crt key",
			configMap: corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name: "ca-cert",
				},
				Data: map[string]string{
					"other.crt": tlsCA,
				},
			},
			want:    nil,
			wantErr: true,
		},
		{
			name: "empty ca.crt key",
			configMap: corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name: "ca-cert",
				},
				Data: map[string]string{
					"ca.crt": "",
				},
			},
			want:    nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CAFromConfigMap(tt.configMap)
			if (err != nil) != tt.wantErr {
				t.Errorf("CAFromConfigMap() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CAFromConfigMap() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTLSClientConfigWithCA(t *testing.T) {
	// Generate a valid CA certificate using the existing test helper
	testSecret := validTlsSecret(t, true)
	tlsCA := string(testSecret.Data[CACrtKey])

	tests := []struct {
		name     string
		caBytes  []byte
		url      string
		wantErr  bool
		checkTLS bool
	}{
		{
			name:     "valid CA certificate",
			caBytes:  []byte(tlsCA),
			url:      "https://example.com",
			wantErr:  false,
			checkTLS: true,
		},
		{
			name:     "valid CA certificate without URL",
			caBytes:  []byte(tlsCA),
			url:      "",
			wantErr:  false,
			checkTLS: true,
		},
		{
			name:     "empty CA bytes",
			caBytes:  []byte{},
			url:      "https://example.com",
			wantErr:  true,
			checkTLS: false,
		},
		{
			name:     "invalid CA certificate",
			caBytes:  []byte("invalid cert data"),
			url:      "https://example.com",
			wantErr:  true,
			checkTLS: false,
		},
		{
			name:     "invalid URL",
			caBytes:  []byte(tlsCA),
			url:      ":::invalid-url",
			wantErr:  true,
			checkTLS: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := TLSClientConfigWithCA(tt.caBytes, tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("TLSClientConfigWithCA() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.checkTLS {
				if got == nil {
					t.Error("TLSClientConfigWithCA() returned nil config")
					return
				}
				if got.MinVersion != tls.VersionTLS12 {
					t.Errorf("TLSClientConfigWithCA() MinVersion = %v, want %v", got.MinVersion, tls.VersionTLS12)
				}
				if got.RootCAs == nil {
					t.Error("TLSClientConfigWithCA() RootCAs is nil")
				}
				if tt.url != "" {
					expectedServerName := "example.com"
					if got.ServerName != expectedServerName {
						t.Errorf("TLSClientConfigWithCA() ServerName = %v, want %v", got.ServerName, expectedServerName)
					}
				}
			}
		})
	}
}
