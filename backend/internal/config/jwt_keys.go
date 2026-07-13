package config

import (
	"bytes"
	"crypto/ed25519"
	"crypto/subtle"
	"crypto/x509"
	"encoding/pem"
	"fmt"
)

const jwtSigningPrivateKeyFileEnvironment = "ASCENDANY_JWT_SIGNING_PRIVATE_KEY_FILE"
const jwtVerificationPublicKeyFileEnvironment = "ASCENDANY_JWT_VERIFICATION_PUBLIC_KEY_FILE"

func loadJWTSigningPrivateKey(lookup LookupEnv, readFile ReadFile) (ed25519.PrivateKey, error) {
	data, err := readCredentialBytes(lookup, readFile, jwtSigningPrivateKeyFileEnvironment)
	if err != nil {
		return nil, err
	}
	block, rest := pem.Decode(data)
	if block == nil || len(rest) != 0 || block.Type != "PRIVATE KEY" || len(block.Headers) != 0 {
		return nil, fmt.Errorf("%s must contain one canonical PKCS#8 Ed25519 PRIVATE KEY PEM block", jwtSigningPrivateKeyFileEnvironment)
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%s must contain a valid PKCS#8 Ed25519 private key", jwtSigningPrivateKeyFileEnvironment)
	}
	privateKey, ok := parsed.(ed25519.PrivateKey)
	if !ok || len(privateKey) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("%s must contain an Ed25519 private key", jwtSigningPrivateKeyFileEnvironment)
	}
	canonicalPrivateKey := ed25519.NewKeyFromSeed(privateKey[:ed25519.SeedSize])
	if subtle.ConstantTimeCompare(privateKey, canonicalPrivateKey) != 1 {
		return nil, fmt.Errorf("%s contains an internally inconsistent Ed25519 private key", jwtSigningPrivateKeyFileEnvironment)
	}
	canonicalDER, err := x509.MarshalPKCS8PrivateKey(canonicalPrivateKey)
	if err != nil {
		return nil, fmt.Errorf("%s Ed25519 private key cannot be normalized", jwtSigningPrivateKeyFileEnvironment)
	}
	canonicalPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: canonicalDER})
	if !bytes.Equal(data, canonicalPEM) {
		return nil, fmt.Errorf("%s must use canonical PKCS#8 PEM encoding", jwtSigningPrivateKeyFileEnvironment)
	}
	return append(ed25519.PrivateKey(nil), canonicalPrivateKey...), nil
}

func loadJWTVerificationPublicKey(lookup LookupEnv, readFile ReadFile) (ed25519.PublicKey, error) {
	data, err := readCredentialBytes(lookup, readFile, jwtVerificationPublicKeyFileEnvironment)
	if err != nil {
		return nil, err
	}
	block, rest := pem.Decode(data)
	if block == nil || len(rest) != 0 || block.Type != "PUBLIC KEY" || len(block.Headers) != 0 {
		return nil, fmt.Errorf("%s must contain one canonical PKIX Ed25519 PUBLIC KEY PEM block", jwtVerificationPublicKeyFileEnvironment)
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%s must contain a valid PKIX Ed25519 public key", jwtVerificationPublicKeyFileEnvironment)
	}
	publicKey, ok := parsed.(ed25519.PublicKey)
	if !ok || len(publicKey) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("%s must contain an Ed25519 public key", jwtVerificationPublicKeyFileEnvironment)
	}
	canonicalDER, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		return nil, fmt.Errorf("%s Ed25519 public key cannot be normalized", jwtVerificationPublicKeyFileEnvironment)
	}
	canonicalPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: canonicalDER})
	if !bytes.Equal(data, canonicalPEM) {
		return nil, fmt.Errorf("%s must use canonical PKIX PEM encoding", jwtVerificationPublicKeyFileEnvironment)
	}
	return append(ed25519.PublicKey(nil), publicKey...), nil
}

func readCredentialBytes(lookup LookupEnv, readFile ReadFile, environmentName string) ([]byte, error) {
	path, err := requiredTrimmed(lookup, environmentName)
	if err != nil {
		return nil, err
	}
	data, err := readFile(path)
	if err != nil {
		return nil, fmt.Errorf("%s cannot be read", environmentName)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("%s must not be empty", environmentName)
	}
	return append([]byte(nil), data...), nil
}
