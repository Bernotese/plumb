// Package secret is the encrypted secret store (docs/ARCHITECTURE.md §7,
// §12, D-030, D-086, D-087).
//
// The master key is 32 bytes in a key file (mode 0600) that only
// `plumb key init` or `plumb key restore` writes. HKDF-SHA256 derives one
// key per purpose. Secrets are AES-256-GCM with a random nonce per write;
// the additional data binds purpose, name and version. The store can
// write, list and delete secrets. Only Resolve returns values, and only
// package plan calls it.
package secret
