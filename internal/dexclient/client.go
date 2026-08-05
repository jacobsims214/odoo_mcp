// Package dexclient wraps Dex's gRPC admin API for dynamic client registration.
// It connects to Dex's gRPC listener using mutual TLS and provides methods
// for creating OAuth2 clients.
package dexclient

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"

	"github.com/dexidp/dex/api/v2"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// Config controls how the client connects to Dex's gRPC listener.
type Config struct {
	Addr     string // Dex gRPC address, e.g. "dex-grpc.namespace.svc.cluster.local:5557"
	CAFile   string // CA cert file that signed Dex's gRPC server cert
	CertFile string // Client certificate file for mTLS
	KeyFile  string // Client key file for mTLS
}

// Client wraps the Dex gRPC connection and provides client management operations.
type Client struct {
	conn *grpc.ClientConn
	api  api.DexClient
}

// New dials Dex's gRPC API and returns a ready-to-use Client.
// If CAFile, CertFile, and KeyFile are all empty, an insecure (plaintext) gRPC
// connection is established. Otherwise mTLS is used with the provided certs.
func New(cfg Config) (*Client, error) {
	if cfg.Addr == "" {
		return nil, fmt.Errorf("dexclient: Addr is required")
	}

	var opts []grpc.DialOption

	if cfg.CAFile == "" && cfg.CertFile == "" && cfg.KeyFile == "" {
		// Insecure connection — no mTLS.
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	} else {
		// mTLS connection.
		caPEM, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("dexclient: read CA cert %s: %w", cfg.CAFile, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("dexclient: failed to parse CA cert %s", cfg.CAFile)
		}

		cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("dexclient: load client cert/key: %w", err)
		}

		creds := credentials.NewTLS(&tls.Config{
			Certificates: []tls.Certificate{cert},
			RootCAs:      pool,
		})
		opts = append(opts, grpc.WithTransportCredentials(creds))
	}

	conn, err := grpc.NewClient(cfg.Addr, opts...)
	if err != nil {
		return nil, fmt.Errorf("dexclient: dial dex grpc at %s: %w", cfg.Addr, err)
	}

	return &Client{
		conn: conn,
		api:  api.NewDexClient(conn),
	}, nil
}

// CreateClient registers a new public OAuth2 client in Dex with the given
// redirect URIs and display name. The client is created as a public client
// (no client secret, PKCE flow). It returns the generated client ID.
func (c *Client) CreateClient(ctx context.Context, redirectURIs []string, name string) (string, error) {
	req := &api.CreateClientReq{
		Client: &api.Client{
			RedirectUris: redirectURIs,
			Name:         name,
			Public:       true,
		},
	}

	resp, err := c.api.CreateClient(ctx, req)
	if err != nil {
		return "", fmt.Errorf("dexclient: CreateClient RPC: %w", err)
	}

	if resp.Client == nil {
		return "", fmt.Errorf("dexclient: CreateClient returned nil client")
	}

	return resp.Client.Id, nil
}

// CreatePassword creates a password entry in Dex for the given email and
// plaintext password. The password is bcrypt-hashed before sending to Dex's
// gRPC API. Returns the email (used as the Dex user ID on success).
func (c *Client) CreatePassword(ctx context.Context, email, password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("dexclient: bcrypt hash: %w", err)
	}

	req := &api.CreatePasswordReq{
		Password: &api.Password{
			Email:    email,
			Username: email,
			UserId:   email,
			Hash:     hash,
		},
	}

	resp, err := c.api.CreatePassword(ctx, req)
	if err != nil {
		return "", fmt.Errorf("dexclient: CreatePassword RPC: %w", err)
	}

	if resp.AlreadyExists {
		return "", fmt.Errorf("dexclient: password already exists for %s", email)
	}

	return email, nil
}

// CreatePasswordRaw creates a Dex password entry using a pre-built CreatePasswordReq.
// This is used by the admin handler which handles its own bcrypt hashing.
func (c *Client) CreatePasswordRaw(ctx context.Context, req *api.CreatePasswordReq) (*api.CreatePasswordResp, error) {
	resp, err := c.api.CreatePassword(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("dexclient: CreatePassword RPC: %w", err)
	}
	return resp, nil
}

// DeletePassword deletes a password entry in Dex by email.
func (c *Client) DeletePassword(ctx context.Context, email string) error {
	req := &api.DeletePasswordReq{
		Email: email,
	}

	resp, err := c.api.DeletePassword(ctx, req)
	if err != nil {
		return fmt.Errorf("dexclient: DeletePassword RPC: %w", err)
	}

	if resp.NotFound {
		return fmt.Errorf("dexclient: password not found for %s", email)
	}

	return nil
}

// NewClient is a convenience constructor that accepts positional args
// matching the mcpoauth main.go calling convention.
func NewClient(ctx context.Context, addr, caFile, certFile, keyFile string) (*Client, error) {
	return New(Config{
		Addr:     addr,
		CAFile:   caFile,
		CertFile: certFile,
		KeyFile:  keyFile,
	})
}

// Close closes the gRPC connection to Dex.
func (c *Client) Close() error {
	return c.conn.Close()
}