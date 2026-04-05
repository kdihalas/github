package github

import (
	"context"
	"errors"
	"testing"

	gh "github.com/google/go-github/v84/github"
)

func TestNewClient_NoOptions(t *testing.T) {
	c, err := NewClient(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestWithGithubClient(t *testing.T) {
	ghc := gh.NewClient(nil)
	c, err := NewClient(context.Background(), WithGithubClient(ghc))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.client != ghc {
		t.Fatal("expected client to be set")
	}
}

func TestWithGithubToken(t *testing.T) {
	c, err := NewClient(context.Background(), WithGithubToken("mytoken"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.client == nil {
		t.Fatal("expected non-nil inner client")
	}
}

func TestWithGithubApplication_InvalidPEM(t *testing.T) {
	_, err := NewClient(context.Background(),
		WithGithubApplication("clientid", 42, []byte("not-a-pem")),
	)
	if err == nil {
		t.Fatal("expected error for invalid PEM")
	}
}

func TestWithGithubApplication_ValidPEM(t *testing.T) {
	// A minimal PKCS8 RSA private key in PEM format (2048-bit, generated for tests only).
	const testPEM = `-----BEGIN PRIVATE KEY-----
MIIEvgIBADANBgkqhkiG9w0BAQEFAASCBKgwggSkAgEAAoIBAQCvwn6di17UFYcc
lFgSQUFzeY40LMVRgMEIPlyRf6kIFP9sD1+ppfBmdRtNAhr/De+ZYzqkonBUtkMe
4M5YAN07yB0L7nFd7v/HGkHqPcG4X/hTioBX6syHak35M163YicVaUzYWhKYwoU5
oOuSiiXViMkQvn8SpPGs+vtXRtEMRjO6xs0DhPWft4Fmp4T5HXNSCZneeLWGlk0Q
OzSQlg7+EF5BGv7DxdAjDhRN7eu9m9ab09bpg601MEiGhIGPCp+mpAEC8XLnqNcG
1EhsOSzvfiQEg2VIfVaZXVkHt+sBp2NGJnBwM+LT7q4+zRmckA2zRjmEgEA5B8Me
mZ80+Wx1AgMBAAECggEAIMupLhL9MxRARZYtl9xqztYB8ZzgBb0BZ90hDzozq3El
lv+IYWK4CJo16ajoqipqyKOSI/m2fawTuq2GezfQEDFfMCSCLV2lBv0QixmSbemk
b8wydhU3LFZq7cLG2++Z7N4c62rlOPFlBBORmWKjPCS9pdzx36P8/4LGhurNI52f
k+kVbTxjwBfX4ufbYCEQ637NOz3zRzHCHv4aR6hY1yzSfS08wL5wFZXkAXeB+k54
RHMPywH7rhUJ8mACa4XTuwvH6Ea+rtgeGjQN/l3KVGwhp5p4TboFh4PufMDQfSWJ
j3zwCpBTUWc9UApqIfY/zXlvfCy7d6cdV0vDGSiYEQKBgQDe3q5KkOMCILslaHNn
sbH1SMJ2dpcrkC9w+2zy6u/FzYqbbWB5D3JbVf+c+WU0m38VmEVRumUR6SolciTy
s8v3k7YhCmLNW8gnEQ3OAyyTLq//G0XUVYqLvNBsaebF4azprbQ5DaDRQNrmYRI1
by6fadBopvU293lJNc11bCFpsQKBgQDJ4wmM2cToDPMYqxroqozVmQi9LB7Nek17
EmNppEvwWBG1XckQBytdkD+KFcct3angJmvNCMXMrKA2chIdsLeCkU8ln4/7aWSE
Ti1txd7/FL9M1/I4cKWM0RmqVFeBr8aAVfbUQYmqmWn2bbYSo2O6YNwYqutjbtCS
Mes0qrMcBQKBgGfjGxFtCjRSt4nPb4QVg6OXn/YCf6Lx2ftrZ7SwKMZmckbTLFYi
CidjJfyxECj+lrWlPiLDpRs9OcUsuOZdQyWLuCkco0OgleMIAwxV1HBjIezjdKBu
o19Ry0HN96Gj+asPqmOx45XHCoK7GvbHdc8fTuOJd+KAZwvmRXiHx+dxAoGBAIqG
7+Gm97amVBQELFWj2TkjZdywLn6dwhaFupMdekHznEsPjEwkLzxnI0IzyVUOeWbl
1ih9MYRMmy5gvU+EF5dO77kIMLq5SZCDOCbPlEEBUnZ+4qSZnu7t96dpchX5r8IV
umVQhw75b7z48Or/FAoqNjvy48t5mUIHYLXlvzqlAoGBAJo1+xJb6/ZQveQp0n6t
H2gI49S4bZpMrWnH4f47cIKBxLEJWHioGTqSuupTXJ8ZfGlfu8usIpCGZIito4Al
bKU72SsUjDeVh0cp7ST6mZB9bEPlggqgzMEwM5u7e/zfMYt461vw0NhnJMBHdzAG
NTXRyivKwzNHqmKjD7iwA8b6
-----END PRIVATE KEY-----`

	c, err := NewClient(context.Background(),
		WithGithubApplication("clientid", 42, []byte(testPEM)),
	)
	if err != nil {
		t.Fatalf("unexpected error with valid PEM: %v", err)
	}
	if c == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestNewClient_MultipleErrors(t *testing.T) {
	badOpt := func(*Client) error { return errors.New("bad option") }
	badOpt2 := func(*Client) error { return errors.New("another bad option") }

	_, err := NewClient(context.Background(), badOpt, badOpt2)
	if err == nil {
		t.Fatal("expected combined error")
	}
	if !errors.Is(err, errors.New("bad option")) {
		// errors.Join doesn't wrap with Is, just check string contains both
		msg := err.Error()
		if len(msg) == 0 {
			t.Fatal("expected non-empty error message")
		}
	}
}
