// Copyright 2021 Chaos Mesh Authors.
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
//

package physicalmachine

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	cryptorand "crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math"
	"math/big"
	"path/filepath"
	"time"

	"github.com/pkg/errors"
	certutil "k8s.io/client-go/util/cert"
	"k8s.io/client-go/util/keyutil"
)

const (
	rsaKeySize    = 2048
	ChaosdPkiName = "chaosd"
	// CertificateBlockType is a possible value for pem.Block.Type.
	CertificateBlockType = "CERTIFICATE"
	// CertificateValidity defines the validity for all the signed certificates generated by kubeadm
	CertificateValidity = time.Hour * 24 * 1825
)

func ParseCertAndKey(certData, keyData []byte) (*x509.Certificate, crypto.Signer, error) {
	caCert, err := ParseCert(certData)
	if err != nil {
		return nil, nil, errors.Wrap(err, "parse certs pem failed")
	}

	caKey, err := ParsePrivateKey(keyData)
	if err != nil {
		return nil, nil, errors.Wrap(err, "parse ca key file failed")
	}
	return caCert, caKey, nil
}

func ParsePrivateKey(data []byte) (crypto.Signer, error) {
	privKey, err := keyutil.ParsePrivateKeyPEM(data)
	if err != nil {
		return nil, fmt.Errorf("error reading private key file: %v", err)
	}

	var key crypto.Signer
	// Allow RSA and ECDSA formats only
	switch k := privKey.(type) {
	case *rsa.PrivateKey:
		key = k
	case *ecdsa.PrivateKey:
		key = k
	default:
		return nil, errors.New("the private key file is neither in RSA nor ECDSA format")
	}
	return key, nil
}

func ParseCert(data []byte) (*x509.Certificate, error) {
	caCerts, err := certutil.ParseCertsPEM(data)
	if err != nil {
		return nil, errors.Wrap(err, "parse certs pem failed")
	}
	return caCerts[0], nil
}

// NewCertAndKey creates new certificate and key by passing the certificate authority certificate and key
func NewCertAndKey(caCert *x509.Certificate, caKey crypto.Signer) (*x509.Certificate, crypto.Signer, error) {
	key, err := NewPrivateKey(x509.RSA)
	if err != nil {
		return nil, nil, errors.Wrap(err, "unable to create private key")
	}

	cert, err := NewSignedCert(key, caCert, caKey, false)
	if err != nil {
		return nil, nil, errors.Wrap(err, "unable to sign certificate")
	}

	return cert, key, nil
}

func NewPrivateKey(keyType x509.PublicKeyAlgorithm) (crypto.Signer, error) {
	if keyType == x509.ECDSA {
		return ecdsa.GenerateKey(elliptic.P256(), cryptorand.Reader)
	}

	return rsa.GenerateKey(cryptorand.Reader, rsaKeySize)
}

// NewSignedCert creates a signed certificate using the given CA certificate and key
func NewSignedCert(key crypto.Signer, caCert *x509.Certificate, caKey crypto.Signer, isCA bool) (*x509.Certificate, error) {
	serial, err := cryptorand.Int(cryptorand.Reader, new(big.Int).SetInt64(math.MaxInt64))
	if err != nil {
		return nil, err
	}

	keyUsage := x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature
	if isCA {
		keyUsage |= x509.KeyUsageCertSign
	}

	notAfter := time.Now().Add(CertificateValidity).UTC()

	certTmpl := x509.Certificate{
		Subject: pkix.Name{
			CommonName: "chaosd.chaos-mesh.org",
		},
		DNSNames:              []string{"chaosd.chaos-mesh.org", "localhost"},
		SerialNumber:          serial,
		NotBefore:             caCert.NotBefore,
		NotAfter:              notAfter,
		KeyUsage:              keyUsage,
		BasicConstraintsValid: true,
		IsCA:                  isCA,
	}
	certDERBytes, err := x509.CreateCertificate(cryptorand.Reader, &certTmpl, caCert, key.Public(), caKey)
	if err != nil {
		return nil, err
	}
	return x509.ParseCertificate(certDERBytes)
}

// WriteCertAndKey stores certificate and key at the specified location
func WriteCertAndKey(pkiPath string, name string, cert *x509.Certificate, key crypto.Signer) error {
	if err := WriteKey(pkiPath, name, key); err != nil {
		return errors.Wrap(err, "couldn't write key")
	}

	return WriteCert(pkiPath, name, cert)
}

// WriteCert stores the given certificate at the given location
func WriteCert(pkiPath, name string, cert *x509.Certificate) error {
	if cert == nil {
		return errors.New("certificate cannot be nil when writing to file")
	}

	certificatePath := pathForCert(pkiPath, name)
	if err := certutil.WriteCert(certificatePath, EncodeCertPEM(cert)); err != nil {
		return errors.Wrapf(err, "unable to write certificate to file %s", certificatePath)
	}

	return nil
}

// WriteKey stores the given key at the given location
func WriteKey(pkiPath, name string, key crypto.Signer) error {
	if key == nil {
		return errors.New("private key cannot be nil when writing to file")
	}

	privateKeyPath := pathForKey(pkiPath, name)
	encoded, err := keyutil.MarshalPrivateKeyToPEM(key)
	if err != nil {
		return errors.Wrapf(err, "unable to marshal private key to PEM")
	}
	if err := keyutil.WriteKey(privateKeyPath, encoded); err != nil {
		return errors.Wrapf(err, "unable to write private key to file %s", privateKeyPath)
	}

	return nil
}

// EncodeCertPEM returns PEM-endcoded certificate data
func EncodeCertPEM(cert *x509.Certificate) []byte {
	block := pem.Block{
		Type:  CertificateBlockType,
		Bytes: cert.Raw,
	}
	return pem.EncodeToMemory(&block)
}

func pathForCert(pkiPath, name string) string {
	return filepath.Join(pkiPath, fmt.Sprintf("%s.crt", name))
}

func pathForKey(pkiPath, name string) string {
	return filepath.Join(pkiPath, fmt.Sprintf("%s.key", name))
}
