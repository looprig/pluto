package packfile

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// digestVersion is the versioned header hashed into every pack digest, so a
// future change to what gets hashed produces a distinguishable digest space.
const digestVersion = "packfile/v1\n"

// lockfileVersion is the token that opens a pack.digest lockfile.
const lockfileVersion = "packfile-digest/v1"

// Digest hashes digestVersion, then each member file's name and sha256, in
// pack.yaml order with pack.yaml itself first. The result is a lowercase hex
// sha256 over that canonical listing: deterministic for a given set of file
// contents and order, and sensitive to reordering pack.yaml's tables list.
func (d *Document) Digest() string {
	var buf bytes.Buffer
	buf.WriteString(digestVersion)

	names := make([]string, 0, len(d.Pack.Tables)+1)
	names = append(names, packFileName)
	names = append(names, d.Pack.Tables...)

	for _, name := range names {
		sum := sha256.Sum256(d.Raw[name])
		fmt.Fprintf(&buf, "%s %s\n", name, hex.EncodeToString(sum[:]))
	}

	final := sha256.Sum256(buf.Bytes())
	return hex.EncodeToString(final[:])
}

// DigestLockfile renders the pack.digest content for writing: the fixed
// "packfile-digest/v1 <revision> <sha256-hex>\n" format VerifyDigest expects.
func DigestLockfile(d *Document) []byte {
	return []byte(fmt.Sprintf("%s %s %s\n", lockfileVersion, d.Pack.Revision, d.Digest()))
}

// VerifyDigest enforces the change-requires-revision-bump rule against the
// committed pack.digest lockfile bytes: the pack's current digest must either
// match the locked digest, or the locked revision must differ from the pack's
// current revision (a bump acknowledging the change). A digest mismatch
// against an unchanged revision is rejected; a malformed lockfile is
// rejected outright.
func VerifyDigest(d *Document, lockfile []byte) error {
	lockRevision, lockHash, err := parseLockfile(lockfile)
	if err != nil {
		return err
	}

	if d.Digest() == lockHash {
		return nil
	}
	if lockRevision == string(d.Pack.Revision) {
		return &Error{
			Path: "pack.digest",
			Reason: fmt.Sprintf(
				"revision bump required: pack contents changed but revision %q was not bumped",
				lockRevision),
		}
	}
	// Hash differs and the revision differs too: the bump has already been
	// acknowledged. The caller is expected to rewrite the lockfile.
	return nil
}

// parseLockfile strictly parses a pack.digest lockfile's fixed
// "packfile-digest/v1 <revision> <sha256-hex>\n" format.
func parseLockfile(lockfile []byte) (revision, hash string, err error) {
	fields := strings.Fields(string(lockfile))
	if len(fields) != 3 || fields[0] != lockfileVersion {
		return "", "", &Error{Path: "pack.digest", Reason: "malformed pack.digest lockfile"}
	}
	revision, hash = fields[1], fields[2]
	if len(hash) != sha256.Size*2 {
		return "", "", &Error{Path: "pack.digest", Reason: "malformed pack.digest lockfile: invalid digest hash"}
	}
	if _, err := hex.DecodeString(hash); err != nil {
		return "", "", &Error{Path: "pack.digest", Reason: "malformed pack.digest lockfile: invalid digest hash"}
	}
	return revision, hash, nil
}
