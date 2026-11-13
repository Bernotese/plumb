// Package backup holds the contract of a Plumb backup file (D-288, D-302):
// its layout, header, names and the files that serve and the host copy
// timer write next to it. T-112 adds the writer, T-113 the reader and
// "plumb restore". This file has no crypto code; it fixes the format so
// the writer, the reader, the installer and the tests agree.
//
// Layout of a backup file:
//
//	Magic (8 bytes) | header length (uint32, big endian) | header (JSON, Header)
//	chunk*: ciphertext length (uint32, big endian) | ciphertext (AES-256-GCM, tag at the end)
//
// The plaintext of all chunks, in order, is a tar stream (ustar, regular
// files only) with exactly the entries of PlainEntries, in that order.
//
// Keys: the master key (32 bytes, the recovery key) is the HKDF-SHA256
// input. The data key of one file is HKDF(master, salt = Header.Salt,
// info = KDFInfoData), 32 bytes. The key fingerprint is the hex of
// HKDF(master, salt = none, info = KDFInfoFingerprint), FingerprintBytes
// bytes; it is the same for every backup of one master key, so the
// installer can check a typed recovery key before any removal (D-290).
//
// Chunk i (from 0) has the nonce NoncePrefix (7 bytes) | i (uint32, big
// endian) | last (1 byte: 1 for the last chunk, else 0), and the AAD
// SHA-256(Magic | header length | header). Each chunk holds ChunkSize
// plaintext bytes, the last one 0 … ChunkSize. A reader refuses: a wrong
// magic or Version, a header over MaxHeaderBytes or with unknown fields,
// a ciphertext length over ChunkSize + 16, a chunk that does not open, a
// last flag before the end, data after the last chunk, and a file that
// ends without a last chunk. So nothing can be cut, reordered, swapped
// between files or changed, and the header is authenticated by the
// first chunk.
package backup

import "regexp"

// Magic starts every backup file.
const Magic = "PLUMBBK\x00"

// Version is the format version in Header.V. A reader refuses another one.
const Version = 1

// Format limits and parameters.
const (
	MaxHeaderBytes   = 4096
	ChunkSize        = 1 << 20 // plaintext bytes per chunk
	SaltBytes        = 32
	NoncePrefixBytes = 7
	FingerprintBytes = 16
	Cipher           = "aes-256-gcm"
	KDF              = "hkdf-sha256"
)

// HKDF info labels. Never reuse them for another key.
const (
	KDFInfoData        = "plumb backup v1 data"
	KDFInfoFingerprint = "plumb backup v1 fingerprint"
)

// Header is the clear header of a backup file. It holds no secret. It is
// authenticated (AAD of every chunk), but a reader may show it before the
// first chunk opens only as "claimed".
type Header struct {
	V            int    `json:"v"`             // Version
	CreatedAt    string `json:"created_at"`    // RFC 3339, UTC
	PlumbVersion string `json:"plumb_version"` // version of the serve that wrote it
	SchemaVer    int    `json:"schema"`        // number of the last migration of the database
	// InstallID, Node, Pool and AgentURL are the install that wrote the
	// backup (PLUMB_INSTALL_ID, PLUMB_PVE_NODE, PLUMB_PVE_POOL and the
	// URL that the agents pin). --restore checks them (D-290 N7, D-304).
	InstallID string `json:"install_id"`
	Node      string `json:"node"`
	Pool      string `json:"pool"`
	AgentURL  string `json:"agent_url"`
	// Crypto parameters: fixed values, written so that a reader of a
	// later format can say why it refuses.
	KDF         string `json:"kdf"`          // KDF
	Cipher      string `json:"cipher"`       // Cipher
	ChunkSize   int    `json:"chunk_size"`   // ChunkSize
	Salt        string `json:"salt"`         // base64 (std, padded) of SaltBytes random bytes
	NoncePrefix string `json:"nonce_prefix"` // base64 of NoncePrefixBytes random bytes
	KeyFP       string `json:"key_fp"`       // lowercase hex, FingerprintBytes bytes
}

// The entries of the plaintext tar stream, in this order. The database is
// a consistent online snapshot (VACUUM INTO); ca.key keeps mode 0600. The
// agent link server certificate and key are not in it: serve makes new
// ones from the CA. plumb.env (the Proxmox token secret) and the master
// key are never in it.
const (
	EntryManifest = "manifest.json" // Manifest
	EntryDB       = "plumb.db"
	EntryCACert   = "link/ca.crt"
	EntryCAKey    = "link/ca.key"
)

// PlainEntries is the fixed list of tar entries.
var PlainEntries = []string{EntryManifest, EntryDB, EntryCACert, EntryCAKey}

// Manifest is the first tar entry. It repeats the header fields that the
// restore needs after decryption and adds the sizes, so a cut tar is seen.
type Manifest struct {
	V         int              `json:"v"`
	CreatedAt string           `json:"created_at"`
	InstallID string           `json:"install_id"`
	SchemaVer int              `json:"schema"`
	Sizes     map[string]int64 `json:"sizes"` // entry name → bytes
}

// File names in the backup directory of serve (DirName under the data
// dir, 0700, owner plumb). A backup is written as TempPrefix+name, synced
// and renamed; then name+SumSuffix (sha256sum format: "<hex>  <name>\n");
// then PointerName (Pointer), each by temp file + rename. A reader that
// sees PointerName never sees half a backup (D-288 N1).
const (
	DirName     = "backups"
	TempPrefix  = ".tmp-"
	SumSuffix   = ".sha256"
	PointerName = "latest"
	FileSuffix  = ".pbk"
)

// NameRE is the only form of a backup file name: "plumb-" + the UTC time
// as 20060102T150405Z + ".pbk", with "-<n>" before the suffix when two
// backups start in the same second. The host copy timer refuses every
// other name and never uses a name from the container as a host path
// (D-289 N1).
var NameRE = regexp.MustCompile(`^plumb-[0-9TZ-]+\.pbk$`)

// NameLayout is the time layout of a backup name.
const NameLayout = "20060102T150405Z"

// Pointer is the file PointerName: the newest complete backup.
type Pointer struct {
	Name   string `json:"name"`   // NameRE
	Size   int64  `json:"size"`   // bytes
	SHA256 string `json:"sha256"` // lowercase hex of the whole file
}

// CopyStatusPath is where the host copy timer puts its status in the
// container (D-289 N2): the installer makes /var/lib/plumb-copy owned by
// root, mode 0755, and the file is 0644 root, so user plumb reads it and
// cannot plant a link there. serve reads it for GET /backups and treats it
// as host data (root on the host wrote it), but still checks its shape.
const CopyStatusPath = "/var/lib/plumb-copy/status.json"

// CopyStatus is the content of CopyStatusPath.
type CopyStatus struct {
	V       int    `json:"v"`      // 1
	At      string `json:"at"`     // RFC 3339 UTC, end of the run
	Result  string `json:"result"` // CopyOK or CopyFailed
	Name    string `json:"name"`   // the backup copied (NameRE), "" on failure before the pull
	SHA256  string `json:"sha256"`
	Storage string `json:"storage"` // the Proxmox storage id
	Message string `json:"message"` // short, for humans; at most MaxCopyMessage bytes
}

// Results of a copy run.
const (
	CopyOK     = "ok"
	CopyFailed = "failed"
)

// MaxCopyMessage is the longest CopyStatus.Message that serve shows.
const MaxCopyMessage = 512

// CopyStaleHours: GET /backups shows the copy as "stale" when the last
// successful copy is older than this.
const CopyStaleHours = 36
