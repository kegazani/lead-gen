package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"lead-gen/backend/platform/migrate"
)

const releaseStateDir = "/run/leadgen-release"
const releaseStateMarker = "initialized-v1\n"

var releaseDigestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

// releasePayload — стабильная версия 1 подписанного доказательства. Порядок
// полей структуры задаёт единственное JSON-представление, используемое для
// подписи и проверки; декодирование входного JSON запрещает неизвестные поля.
type releasePayload struct {
	Version                 int               `json:"version"`
	Purpose                 string            `json:"purpose"`
	DeploymentID            string            `json:"deployment_id"`
	Environment             string            `json:"environment"`
	Target                  string            `json:"target"`
	DaemonID                string            `json:"daemon_id"`
	LedgerID                string            `json:"ledger_id"`
	Generation              string            `json:"generation"`
	ReleaseNonce            string            `json:"release_nonce"`
	Sequence                uint64            `json:"sequence"`
	BaseActive              string            `json:"base_active_generation"`
	BundleFiles             []string          `json:"bundle_files"`
	RuntimeRoles            map[string]string `json:"runtime_roles"`
	CandidateManifestSHA256 string            `json:"candidate_manifest_sha256,omitempty"`
	ImageDigest             string            `json:"image_digest"`
	IssuedAt                string            `json:"issued_at"`
	ExpiresAt               string            `json:"expires_at"`
	KeyID                   string            `json:"key_id"`
}

type signedAttestation struct {
	Payload   json.RawMessage `json:"payload"`
	Signature string          `json:"signature"`
}

type releaseOwner struct {
	Version      int                   `json:"version"`
	OwnerAttempt string                `json:"owner_attempt"`
	ReleaseNonce string                `json:"release_nonce"`
	Identity     releaseLedgerIdentity `json:"identity"`
	PID          int                   `json:"pid"`
	StartedAt    string                `json:"started_at"`
	Heartbeat    string                `json:"heartbeat"`
}

func releaseHelperMode(args []string) (bool, error) {
	if len(args) == 0 {
		return false, nil
	}
	switch args[0] {
	case "-release-lock-holder":
		return true, runReleaseLockHolder(args[1:])
	case "-release-state-init":
		return true, runReleaseStateInit(args[1:])
	case "-release-owner":
		return true, runReleaseOwner(args[1:])
	case "-release-ledger-read":
		return true, runReleaseLedgerRead(args[1:])
	case "-release-ledger-write":
		return true, errors.New("release ledger direct write disabled; use lock-holder IPC")
	case "-release-ledger-client":
		return true, runReleaseLedgerClient(args[1:])
	case "-release-attest":
		return true, runReleaseAttest(args[1:])
	case "-release-sign-candidate":
		return true, runReleaseSignCandidate(args[1:])
	case "-release-sign-g1-enrollment", "-release-sign-backup-evidence":
		return true, errors.New("initial G1 signing: typed observed-input/backup evidence CLI is not enabled")
	case "-release-bootstrap-g1":
		return true, runInitialGenesisHelper(args[1:])
	case "-release-stage-candidate":
		return true, runReleaseStageCandidate(args[1:])
	case "-release-signing-key-check":
		return true, runReleaseSigningKeyCheck(args[1:])
	case "-release-trust-key-check":
		return true, runReleaseTrustKeyCheck(args[1:])
	case "-release-verify-attestation":
		return true, runReleaseVerifyAttestation(args[1:])
	case "-release-activation-gate":
		return true, runReleaseActivationGate(args[1:])
	default:
		return false, nil
	}
}

// runReleaseStateInit подготавливает только пустой metadata volume до запуска
// nonroot holder. Он не читает и не меняет ledger; существующий state никогда не
// нормализуется этим режимом.
func runReleaseStateInit(args []string) error {
	if len(args) != 0 || os.Geteuid() != 0 {
		return errors.New("release state init: root-only empty-volume initialization required")
	}
	info, err := os.Lstat(releaseStateDir)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("release state init: invalid state directory")
	}
	entries, err := os.ReadDir(releaseStateDir)
	if err != nil {
		return errors.New("release state init: state directory unreadable")
	}
	if len(entries) != 0 {
		return errors.New("release state init: refusing to change nonempty state volume")
	}
	directoryFD, err := syscall.Open(releaseStateDir, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return errors.New("release state init: directory open failed")
	}
	defer syscall.Close(directoryFD)
	markerFD, err := unix.Openat(directoryFD, "initialized", unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return errors.New("release state init: durable initialization marker creation failed")
	}
	if err := unix.Fchown(markerFD, 65532, 65532); err != nil {
		unix.Close(markerFD)
		return errors.New("release state init: marker owner initialization failed")
	}
	if err := unix.Fchmod(markerFD, 0o600); err != nil {
		unix.Close(markerFD)
		return errors.New("release state init: marker mode initialization failed")
	}
	if _, err := unix.Write(markerFD, []byte(releaseStateMarker)); err != nil {
		unix.Close(markerFD)
		return errors.New("release state init: marker write failed")
	}
	if err := unix.Fsync(markerFD); err != nil {
		unix.Close(markerFD)
		return errors.New("release state init: marker sync failed")
	}
	if err := unix.Close(markerFD); err != nil {
		return errors.New("release state init: marker close failed")
	}
	if err := syscall.Fchown(directoryFD, 65532, 65532); err != nil {
		return errors.New("release state init: owner initialization failed")
	}
	if err := syscall.Fchmod(directoryFD, 0o700); err != nil {
		return errors.New("release state init: mode initialization failed")
	}
	var initialized unix.Stat_t
	if err := unix.Fstat(directoryFD, &initialized); err != nil || !validReleaseStateDirectoryStat(&initialized) {
		return errors.New("release state init: initialized directory permissions did not persist")
	}
	if err := syscall.Fsync(directoryFD); err != nil {
		return errors.New("release state init: directory sync failed")
	}
	return nil
}

// runReleaseLockHolder захватывает Linux flock и не снимает его по таймауту.
// Единственный способ завершить процесс — явный сигнал от последующего
// activate/recover workflow после сверки runtime и durable ledger.
func runReleaseLockHolder(args []string) error {
	if len(args) != 6 {
		return errors.New("release lock-holder: invalid owner/identity arguments")
	}
	nonce, ownerAttempt := args[0], args[1]
	identity := releaseLedgerIdentity{Environment: args[2], Deployment: args[3], DaemonID: args[4], LedgerID: args[5]}
	stateDir := releaseStateDir
	if !safeReleaseID.MatchString(nonce) || !safeReleaseID.MatchString(ownerAttempt) || !validLedgerIdentity(identity) {
		return errors.New("release lock-holder: required release identity missing")
	}
	if err := verifyReleaseStateDirectory(stateDir); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(stateDir, "release.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return errors.New("release lock-holder: lock file недоступен")
	}
	defer lock.Close()
	if err := verifyReleaseLockFile(lock); err != nil {
		return err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errors.New("release lock-holder: удалённый lock уже удерживается")
	}
	socketPath := filepath.Join(stateDir, "ledger.sock")
	if existing, err := os.Lstat(socketPath); err == nil {
		if err := verifyReleaseSocketInfo(existing); err != nil {
			return errors.New("release lock-holder: stale IPC socket has unsafe owner/mode/type")
		}
		// Удалять остаток IPC-пути можно только после успешного remote flock.
		if err := os.Remove(socketPath); err != nil {
			return errors.New("release lock-holder: stale IPC socket cannot be removed")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("release lock-holder: IPC path inspect failed")
	}
	owner := releaseOwner{
		Version: 1, OwnerAttempt: ownerAttempt, ReleaseNonce: nonce, Identity: identity,
		PID: os.Getpid(), StartedAt: time.Now().UTC().Format(time.RFC3339),
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketPath, Net: "unix"})
	if err != nil {
		return errors.New("release lock-holder: IPC socket listen failed")
	}
	if err := os.Chmod(socketPath, 0o660); err != nil {
		listener.Close()
		return errors.New("release lock-holder: IPC socket permissions failed")
	}
	listener.SetUnlinkOnClose(true)
	if err := verifyReleaseIPCPath(stateDir, socketPath); err != nil {
		listener.Close()
		return err
	}
	if err := writeReleaseOwner(stateDir, owner); err != nil {
		listener.Close()
		return err
	}
	stopHeartbeat := make(chan struct{})
	heartbeatFailure := make(chan error, 1)
	heartbeatOwner := owner
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stopHeartbeat:
				return
			case <-ticker.C:
				heartbeatOwner.Heartbeat = time.Now().UTC().Format(time.RFC3339)
				if err := writeReleaseOwner(stateDir, heartbeatOwner); err != nil {
					heartbeatFailure <- err
					_ = listener.Close()
					return
				}
			}
		}
	}()
	defer close(stopHeartbeat)
	return serveReleaseLedger(listener, lock, stateDir, owner, heartbeatFailure)
}

func serveReleaseLedger(listener *net.UnixListener, lock *os.File, stateDir string, owner releaseOwner, heartbeatFailure <-chan error) error {
	for {
		connection, err := listener.AcceptUnix()
		if err != nil {
			select {
			case heartbeatErr := <-heartbeatFailure:
				return heartbeatErr
			default:
			}
			if errors.Is(err, net.ErrClosed) {
				return errors.New("release lock-holder: IPC listener closed")
			}
			continue
		}
		serveReleaseLedgerConnection(connection, lock, stateDir, owner)
	}
}

func writeReleaseOwner(dir string, owner releaseOwner) error {
	owner.Heartbeat = time.Now().UTC().Format(time.RFC3339)
	data, err := json.Marshal(owner)
	if err != nil {
		return errors.New("release lock-holder: owner marker serialization failed")
	}
	return atomicReleaseWriteMode(dir, "owner.json", data, 0o600)
}

func verifyReleaseStateDirectory(path string) error {
	if filepath.Clean(path) != releaseStateDir {
		return errors.New("release IPC: unexpected state directory")
	}
	return verifyReleaseStateDirectoryAt(path)
}

func verifyReleaseStateDirectoryAt(path string) error {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return errors.New("release IPC: state directory missing, symlinked or inaccessible")
	}
	defer unix.Close(fd)
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil || !validReleaseStateDirectoryStat(&stat) {
		if err != nil {
			return errors.New("release IPC: state directory metadata unavailable")
		}
		return fmt.Errorf("release IPC: state directory owner/GID/mode mismatch (uid=%d gid=%d mode=%#o)",
			stat.Uid, stat.Gid, stat.Mode&0o7777)
	}
	markerFD, err := unix.Openat(fd, "initialized", unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return errors.New("release IPC: state directory initialization marker missing")
	}
	defer unix.Close(markerFD)
	var markerStat unix.Stat_t
	if err := unix.Fstat(markerFD, &markerStat); err != nil || markerStat.Mode&unix.S_IFMT != unix.S_IFREG || markerStat.Uid != 65532 || markerStat.Gid != 65532 || markerStat.Mode&0o7777 != 0o600 {
		return errors.New("release IPC: state directory initialization marker permissions invalid")
	}
	marker := make([]byte, len(releaseStateMarker))
	if _, err := unix.Read(markerFD, marker); err != nil || string(marker) != releaseStateMarker {
		return errors.New("release IPC: state directory initialization marker invalid")
	}
	return nil
}

func validReleaseStateDirectoryStat(stat *unix.Stat_t) bool {
	return stat != nil && stat.Mode&unix.S_IFMT == unix.S_IFDIR && stat.Uid == 65532 && stat.Gid == 65532 && stat.Mode&0o7777 == 0o700
}

func verifyReleaseLockFile(lock *os.File) error {
	var stat unix.Stat_t
	if err := unix.Fstat(int(lock.Fd()), &stat); err != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Uid != 65532 || stat.Gid != 65532 || stat.Mode&0o7777 != 0o600 {
		return errors.New("release IPC: lock file owner/GID/mode mismatch")
	}
	return nil
}

func verifyReleaseSocketInfo(info os.FileInfo) error {
	if info == nil || info.Mode()&os.ModeSymlink != 0 || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0o660 {
		return errors.New("invalid Unix socket type/mode")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !validReleaseSocketStat(stat) {
		return errors.New("invalid Unix socket owner/GID")
	}
	return nil
}

func validReleaseSocketStat(stat *syscall.Stat_t) bool {
	return stat != nil && stat.Mode&syscall.S_IFMT == syscall.S_IFSOCK && stat.Uid == 65532 && stat.Gid == 65532 && stat.Mode&0o7777 == 0o660
}

func verifyReleaseIPCPath(stateDir, socketPath string) error {
	if filepath.Clean(stateDir) != releaseStateDir {
		return errors.New("release IPC: unexpected state directory")
	}
	return verifyReleaseIPCPathAt(stateDir, socketPath)
}

func verifyReleaseIPCPathAt(stateDir, socketPath string) error {
	if err := verifyReleaseStateDirectoryAt(stateDir); err != nil {
		return err
	}
	info, err := os.Lstat(socketPath)
	if err != nil || verifyReleaseSocketInfo(info) != nil {
		return errors.New("release IPC: socket owner/GID/mode/type mismatch")
	}
	return nil
}

func atomicReleaseWrite(dir, name string, data []byte) error {
	return atomicReleaseWriteMode(dir, name, data, 0o600)
}

func atomicReleaseWriteMode(dir, name string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(dir, ".release-tmp-")
	if err != nil {
		return errors.New("release state: temporary file create failed")
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return errors.New("release state: temporary file permissions failed")
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return errors.New("release state: temporary file write failed")
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return errors.New("release state: temporary file sync failed")
	}
	if err := tmp.Close(); err != nil {
		return errors.New("release state: temporary file close failed")
	}
	if err := os.Rename(tmpName, filepath.Join(dir, name)); err != nil {
		return errors.New("release state: atomic rename failed")
	}
	directory, err := os.Open(dir)
	if err != nil {
		return errors.New("release state: directory open failed")
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return errors.New("release state: directory sync failed")
	}
	return nil
}

type releaseLedger struct {
	Version          int                      `json:"version"`
	Identity         releaseLedgerIdentity    `json:"identity"`
	Revision         uint64                   `json:"revision"`
	Sequence         uint64                   `json:"sequence"`
	ActiveGeneration string                   `json:"active_generation"`
	Releases         map[string]releaseRecord `json:"releases"`
}

type releaseLedgerIdentity struct {
	Environment string `json:"environment"`
	Deployment  string `json:"deployment_id"`
	DaemonID    string `json:"daemon_id"`
	LedgerID    string `json:"ledger_id"`
}

type releaseRecord struct {
	Generation              string                 `json:"generation"`
	Nonce                   string                 `json:"nonce"`
	Sequence                uint64                 `json:"sequence"`
	BaseActive              string                 `json:"base_active_generation"`
	State                   string                 `json:"state"`
	Target                  string                 `json:"target"`
	ImageDigest             string                 `json:"image_digest"`
	Manifest                []string               `json:"manifest_files"`
	DBJobs                  map[string]string      `json:"db_job_results,omitempty"`
	Proofs                  map[string]proofRecord `json:"attestations,omitempty"`
	CandidateManifestName   string                 `json:"candidate_manifest_name,omitempty"`
	CandidateManifestSHA256 string                 `json:"candidate_manifest_sha256,omitempty"`
	UpdatedAt               string                 `json:"updated_at"`
}

func runReleaseOwner(args []string) error {
	if len(args) != 1 || filepath.Clean(args[0]) != releaseStateDir {
		return errors.New("release owner: invalid state directory")
	}
	if err := verifyReleaseStateDirectory(args[0]); err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(args[0], "owner.json"))
	if err != nil {
		return errors.New("release owner: marker unavailable")
	}
	var owner releaseOwner
	if err := strictJSON(data, &owner); err != nil || owner.Version != 1 || owner.OwnerAttempt == "" || owner.ReleaseNonce == "" || owner.PID <= 0 {
		return errors.New("release owner: marker invalid")
	}
	_, err = os.Stdout.Write(append(data, '\n'))
	return err
}

func runReleaseLedgerRead(args []string) error {
	if len(args) != 1 || filepath.Clean(args[0]) != releaseStateDir {
		return errors.New("release ledger: invalid state directory")
	}
	if err := verifyReleaseStateDirectory(args[0]); err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(args[0], "ledger.json"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			_, writeErr := os.Stdout.Write([]byte("{\"available\":false}\n"))
			return writeErr
		}
		return errors.New("release ledger: unavailable")
	}
	var ledger releaseLedger
	if err := strictJSON(data, &ledger); err != nil || ledger.Version != 1 || !validLedgerIdentity(ledger.Identity) {
		return errors.New("release ledger: invalid")
	}
	_, err = os.Stdout.Write(append(data, '\n'))
	return err
}

type proofRecord struct {
	KeyID     string `json:"key_id"`
	IssuedAt  string `json:"issued_at"`
	ExpiresAt string `json:"expires_at"`
	Digest    string `json:"payload_digest"`
	Consumed  bool   `json:"consumed"`
}

type ledgerIPCRequest struct {
	Identity         releaseLedgerIdentity `json:"identity"`
	OwnerAttempt     string                `json:"owner_attempt"`
	ReleaseNonce     string                `json:"release_nonce"`
	ExpectedRevision uint64                `json:"expected_revision"`
	Action           string                `json:"action"`
	ProofTarget      string                `json:"proof_target,omitempty"`
	ProofDigest      string                `json:"proof_digest,omitempty"`
	Record           releaseRecord         `json:"record"`
}

type ledgerIPCResponse struct {
	OK       bool           `json:"ok"`
	Revision uint64         `json:"revision"`
	Error    string         `json:"error,omitempty"`
	Ledger   *releaseLedger `json:"ledger,omitempty"`
}

func runReleaseLedgerClient(args []string) error {
	if len(args) != 1 || filepath.Clean(args[0]) != releaseStateDir {
		return errors.New("release ledger IPC: invalid state directory")
	}
	socketPath := filepath.Join(args[0], "ledger.sock")
	if err := verifyReleaseIPCPath(args[0], socketPath); err != nil {
		return errors.New("release ledger IPC: live lock-holder socket unavailable or insecure")
	}
	input, err := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
	if err != nil {
		return errors.New("release ledger IPC: request read failed")
	}
	var request ledgerIPCRequest
	if err := strictJSON(input, &request); err != nil || request.OwnerAttempt == "" || request.ReleaseNonce == "" || !validLedgerIdentity(request.Identity) {
		return errors.New("release ledger IPC: malformed request")
	}
	ownerBytes, err := os.ReadFile(filepath.Join(args[0], "owner.json"))
	if err != nil {
		return errors.New("release ledger IPC: holder marker unavailable")
	}
	var owner releaseOwner
	if err := strictJSON(ownerBytes, &owner); err != nil || owner.PID <= 0 || owner.OwnerAttempt != request.OwnerAttempt || owner.ReleaseNonce != request.ReleaseNonce || owner.Identity != request.Identity {
		return errors.New("release ledger IPC: holder attempt mismatch")
	}
	connection, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: socketPath, Net: "unix"})
	if err != nil {
		return errors.New("release ledger IPC: lock-holder connection failed")
	}
	defer connection.Close()
	if err := verifyReleaseIPCPath(args[0], socketPath); err != nil {
		return errors.New("release ledger IPC: socket changed during connect")
	}
	peerPID, peerUID, peerGID, err := unixPeerCredentials(connection)
	if err != nil || peerPID != owner.PID || peerUID != 65532 || peerGID != 65532 {
		return errors.New("release ledger IPC: peer is not the verified lock-holder process")
	}
	if _, err := connection.Write(input); err != nil {
		return errors.New("release ledger IPC: request send failed")
	}
	if err := connection.CloseWrite(); err != nil {
		return errors.New("release ledger IPC: request close failed")
	}
	responseBytes, err := io.ReadAll(io.LimitReader(connection, 1<<20))
	if err != nil {
		return errors.New("release ledger IPC: response read failed")
	}
	var response ledgerIPCResponse
	if err := strictJSON(responseBytes, &response); err != nil {
		return errors.New("release ledger IPC: malformed response")
	}
	if !response.OK {
		if response.Error == "" {
			return errors.New("release ledger IPC: transition refused")
		}
		return errors.New("release ledger IPC: " + response.Error)
	}
	_, err = os.Stdout.Write(append(responseBytes, '\n'))
	return err
}

func serveReleaseLedgerConnection(connection *net.UnixConn, lock *os.File, stateDir string, holder releaseOwner) {
	defer connection.Close()
	if err := verifyReleaseIPCPathAt(stateDir, filepath.Join(stateDir, "ledger.sock")); err != nil {
		writeLedgerIPCResponse(connection, ledgerIPCResponse{Error: "unsafe parent/socket owner/GID/mode"})
		return
	}
	_, peerUID, peerGID, peerErr := unixPeerCredentials(connection)
	if peerErr != nil || peerUID != 65532 || peerGID != 65532 {
		writeLedgerIPCResponse(connection, ledgerIPCResponse{Error: "client peer credentials rejected"})
		return
	}
	input, err := io.ReadAll(io.LimitReader(connection, 1<<20))
	if err != nil {
		writeLedgerIPCResponse(connection, ledgerIPCResponse{Error: "request read failed"})
		return
	}
	var request ledgerIPCRequest
	if err := strictJSON(input, &request); err != nil {
		writeLedgerIPCResponse(connection, ledgerIPCResponse{Error: "malformed request"})
		return
	}
	if request.OwnerAttempt != holder.OwnerAttempt || request.ReleaseNonce != holder.ReleaseNonce || request.Identity != holder.Identity ||
		(request.Action != "read" && request.Record.Nonce != holder.ReleaseNonce) {
		writeLedgerIPCResponse(connection, ledgerIPCResponse{Error: "holder attempt/release identity mismatch"})
		return
	}
	if err := verifyHolderOwnsLock(lock, stateDir, holder); err != nil {
		writeLedgerIPCResponse(connection, ledgerIPCResponse{Error: "active holder lock proof failed"})
		return
	}
	ledgerPath := filepath.Join(stateDir, "ledger.json")
	ledger := releaseLedger{Version: 1, Identity: holder.Identity, Releases: map[string]releaseRecord{}}
	data, err := os.ReadFile(ledgerPath)
	initialized := false
	if err == nil {
		if err := strictJSON(data, &ledger); err != nil || ledger.Version != 1 || ledger.Identity != holder.Identity || ledger.Releases == nil {
			writeLedgerIPCResponse(connection, ledgerIPCResponse{Error: "corrupt ledger or identity mismatch"})
			return
		}
		initialized = true
	} else if !errors.Is(err, os.ErrNotExist) {
		writeLedgerIPCResponse(connection, ledgerIPCResponse{Error: "ledger read failed"})
		return
	}
	if ledger.Revision != request.ExpectedRevision {
		if request.Action != "read" {
			writeLedgerIPCResponse(connection, ledgerIPCResponse{Revision: ledger.Revision, Error: "revision conflict"})
			return
		}
	}
	if request.Action == "read" {
		if !initialized {
			writeLedgerIPCResponse(connection, ledgerIPCResponse{OK: true, Revision: 0})
			return
		}
		writeLedgerIPCResponse(connection, ledgerIPCResponse{OK: true, Revision: ledger.Revision, Ledger: &ledger})
		return
	}
	if !initialized {
		if request.Action != "new" || request.Record.State != "new" {
			writeLedgerIPCResponse(connection, ledgerIPCResponse{Revision: ledger.Revision, Error: "first transition must reserve candidate"})
			return
		}
		ledger.ActiveGeneration = request.Record.BaseActive
	}
	if err := applyLedgerTransition(&ledger, request.Action, request.ProofTarget, request.ProofDigest, request.Record); err != nil {
		writeLedgerIPCResponse(connection, ledgerIPCResponse{Revision: ledger.Revision, Error: err.Error()})
		return
	}
	ledger.Revision++
	encoded, err := json.Marshal(ledger)
	if err != nil {
		writeLedgerIPCResponse(connection, ledgerIPCResponse{Revision: request.ExpectedRevision, Error: "ledger serialization failed"})
		return
	}
	if err := atomicReleaseWrite(stateDir, "ledger.json", encoded); err != nil {
		writeLedgerIPCResponse(connection, ledgerIPCResponse{Revision: request.ExpectedRevision, Error: "durable ledger write failed"})
		return
	}
	writeLedgerIPCResponse(connection, ledgerIPCResponse{OK: true, Revision: ledger.Revision})
}

func writeLedgerIPCResponse(connection *net.UnixConn, response ledgerIPCResponse) {
	data, err := json.Marshal(response)
	if err == nil {
		_, _ = connection.Write(append(data, '\n'))
	}
}

func verifyHolderOwnsLock(lock *os.File, stateDir string, expected releaseOwner) error {
	if expected.PID != os.Getpid() || expected.OwnerAttempt == "" || expected.ReleaseNonce == "" {
		return errors.New("holder process identity mismatch")
	}
	markerBytes, err := os.ReadFile(filepath.Join(stateDir, "owner.json"))
	if err != nil {
		return errors.New("holder marker unavailable")
	}
	var marker releaseOwner
	if err := strictJSON(markerBytes, &marker); err != nil || marker.PID != os.Getpid() || marker.OwnerAttempt != expected.OwnerAttempt || marker.ReleaseNonce != expected.ReleaseNonce || marker.Identity != expected.Identity {
		return errors.New("holder marker identity mismatch")
	}
	probe, err := os.OpenFile(filepath.Join(stateDir, "release.lock"), os.O_RDWR, 0o600)
	if err != nil {
		return errors.New("holder lock file unavailable")
	}
	defer probe.Close()
	err = syscall.Flock(int(probe.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		_ = syscall.Flock(int(probe.Fd()), syscall.LOCK_UN)
		return errors.New("holder no longer exclusively owns flock")
	}
	if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
		return errors.New("holder flock verification failed")
	}
	return nil
}

func applyLedgerTransition(ledger *releaseLedger, action, proofTarget, proofDigest string, next releaseRecord) error {
	if !safeReleaseID.MatchString(next.Generation) || !safeReleaseID.MatchString(next.Nonce) ||
		!safeReleaseID.MatchString(next.Target) || next.Sequence == 0 || next.State != action ||
		!releaseDigestPattern.MatchString(next.ImageDigest) || !validReleaseManifest(next.Manifest) {
		return errors.New("release ledger: generation, nonce and sequence required")
	}
	if (next.CandidateManifestName == "") != (next.CandidateManifestSHA256 == "") ||
		(next.CandidateManifestName != "" && (next.CandidateManifestName != "candidate-"+next.Nonce+".json" || !releaseDigestPattern.MatchString(next.CandidateManifestSHA256))) {
		return errors.New("release ledger: candidate manifest metadata is invalid")
	}
	if old, exists := ledger.Releases[next.Nonce]; exists {
		if old.Generation != next.Generation || old.Sequence != next.Sequence {
			return errors.New("release ledger: nonce reuse rejected")
		}
		allowed := map[string]map[string]bool{
			"new":                    {"staging": true, "failed": true, "recovery_required": true},
			"staging":                {"staging": true, "verified": true, "failed": true, "recovery_required": true},
			"verified":               {"activation_in_progress": true, "failed": true, "recovery_required": true},
			"activation_in_progress": {"activated": true, "failed": true, "recovery_required": true},
			"failed":                 {"recovery_required": true},
			"recovery_required":      {"activation_in_progress": true, "failed": true},
		}
		if !allowed[old.State][action] {
			return fmt.Errorf("release ledger: transition %s -> %s rejected", old.State, action)
		}
		if old.Generation != next.Generation || old.BaseActive != next.BaseActive || old.Sequence != next.Sequence || old.ImageDigest != next.ImageDigest {
			return errors.New("release ledger: candidate does not match reserved generation/sequence/base")
		}
		if old.CandidateManifestName != "" && (next.CandidateManifestName != old.CandidateManifestName || next.CandidateManifestSHA256 != old.CandidateManifestSHA256) {
			return errors.New("release ledger: signed candidate manifest binding cannot be changed or removed")
		}
		if action == "staging" {
			for job, result := range old.DBJobs {
				if next.DBJobs[job] != result {
					return errors.New("release ledger: prior database result cannot be removed or rewritten")
				}
			}
			for _, filename := range old.Manifest {
				if !containsReleaseFilename(next.Manifest, filename) {
					return errors.New("release ledger: staged manifest cannot shrink")
				}
			}
		}
		if action == "activation_in_progress" {
			proof, ok := old.Proofs[proofTarget]
			updated, updatedOK := next.Proofs[proofTarget]
			if !ok || !updatedOK || proofTarget == "" || proofDigest == "" || proof.Consumed || proof.Digest != proofDigest || updated.Digest != proofDigest || !updated.Consumed || updated.KeyID != proof.KeyID || updated.IssuedAt != proof.IssuedAt || updated.ExpiresAt != proof.ExpiresAt {
				return errors.New("release ledger: activation proof missing, mismatched or already consumed")
			}
		}
		if action == "verified" {
			if next.Target == "intake" {
				if next.CandidateManifestName == "" {
					return errors.New("release ledger: intake candidate requires signed manifest")
				}
				for _, target := range []string{"request-fulfillment", "consoleauth", "integration-platform"} {
					verification := next.DBJobs["candidate-runtime-verify:"+target]
					if !strings.HasPrefix(verification, "succeeded:exit=0") {
						return errors.New("release ledger: all three intake candidate DSNs must verify before attestation")
					}
				}
			} else if _, isDBTarget := dbTargets[next.Target]; isDBTarget {
				return errors.New("release ledger: partial intake target cannot be finalized independently")
			}
			if len(next.Proofs) == 0 {
				return errors.New("release ledger: verified state requires signed attestation metadata")
			}
			for _, proof := range next.Proofs {
				if proof.KeyID == "" || proof.Digest == "" || proof.Consumed {
					return errors.New("release ledger: invalid or already consumed proof metadata")
				}
			}
		}
		if action == "activated" {
			consumed := false
			for _, proof := range old.Proofs {
				consumed = consumed || proof.Consumed
			}
			if !consumed || ledger.ActiveGeneration != next.BaseActive {
				return errors.New("release ledger: activation proof/base generation CAS failed")
			}
			ledger.ActiveGeneration = next.Generation
		}
		next.Manifest = append([]string(nil), next.Manifest...)
		next.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		ledger.Releases[next.Nonce] = next
		return nil
	}
	if action != "new" || next.State != "new" {
		return errors.New("release ledger: first transition must be new")
	}
	for _, old := range ledger.Releases {
		if old.Generation == next.Generation || old.Nonce == next.Nonce {
			return errors.New("release ledger: generation reuse rejected")
		}
	}
	if next.BaseActive != ledger.ActiveGeneration {
		return errors.New("release ledger: active generation compare-and-swap mismatch")
	}
	if next.Sequence != ledger.Sequence+1 {
		return errors.New("release ledger: sequence compare-and-swap mismatch")
	}
	ledger.Sequence = next.Sequence
	next.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	ledger.Releases[next.Nonce] = next
	return nil
}

func containsReleaseFilename(files []string, expected string) bool {
	for _, filename := range files {
		if filename == expected {
			return true
		}
	}
	return false
}

func validLedgerIdentity(identity releaseLedgerIdentity) bool {
	if !safeReleaseID.MatchString(identity.Environment) || !safeReleaseID.MatchString(identity.Deployment) ||
		!releaseDigestPattern.MatchString(identity.DaemonID) || identity.LedgerID == "" {
		return false
	}
	fingerprint := strings.TrimPrefix(identity.DaemonID, "sha256:")
	expected := "lead-gen-release-" + identity.Environment + "-" + identity.Deployment + "-" + fingerprint[:24]
	return identity.LedgerID == expected
}

func validReleaseManifest(files []string) bool {
	for index, name := range files {
		if name == "" || strings.ContainsAny(name, "/\\") || (index > 0 && files[index-1] >= name) {
			return false
		}
	}
	return true
}

func runReleaseAttest(args []string) error {
	if len(args) != 0 {
		return errors.New("release attestation: ключ задаётся только переменной LEADGEN_RELEASE_SIGNING_KEY_FILE")
	}
	privateKey, err := loadReleaseSigningKey()
	if err != nil {
		return err
	}
	input, err := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
	if err != nil {
		return errors.New("release attestation: payload read failed")
	}
	var payload releasePayload
	if err := strictJSON(input, &payload); err != nil {
		return errors.New("release attestation: malformed or unknown payload fields")
	}
	canonical, err := canonicalReleasePayload(payload)
	if err != nil {
		return errors.New("release attestation: payload schema/canonicalization failed")
	}
	sig := ed25519.Sign(privateKey, canonical)
	encoded, err := json.Marshal(signedAttestation{Payload: canonical, Signature: base64.RawStdEncoding.EncodeToString(sig)})
	if err != nil {
		return errors.New("release attestation: encoding failed")
	}
	_, err = os.Stdout.Write(append(encoded, '\n'))
	return err
}

func runReleaseSignCandidate(args []string) error {
	if len(args) != 0 {
		return errors.New("candidate signing: unexpected arguments")
	}
	privateKey, err := loadReleaseSigningKey()
	if err != nil {
		return err
	}
	defer clear(privateKey)
	input, err := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
	if err != nil {
		return errors.New("candidate signing: payload read failed")
	}
	var manifest CandidateManifest
	if err := strictJSON(input, &manifest); err != nil {
		return errors.New("candidate signing: schema rejected")
	}
	canonical, err := canonicalCandidateManifest(manifest)
	if err != nil || !equalBytes(canonical, input) {
		return errors.New("candidate signing: payload is not canonical or policy-compliant")
	}
	signed, err := signCandidateManifest(manifest, privateKey)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(signed)
	return err
}

// runReleaseStageCandidate публикует единственный manifest для nonce в release
// state как неизменяемый artifact. Он принимает только подписанный bytes stream.
func runReleaseStageCandidate(args []string) error {
	if len(args) != 1 || !safeReleaseID.MatchString(args[0]) || os.Geteuid() != 65532 {
		return errors.New("candidate stage: release UID/nonce rejected")
	}
	if err := verifyReleaseStateDirectory(releaseStateDir); err != nil {
		return err
	}
	data, err := io.ReadAll(io.LimitReader(os.Stdin, 1<<20+1))
	if err != nil || len(data) > 1<<20 {
		return errors.New("candidate stage: input size rejected")
	}
	name := "candidate-" + args[0] + ".json"
	dirFD, err := unix.Open(releaseStateDir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return errors.New("candidate stage: state directory open failed")
	}
	defer unix.Close(dirFD)
	fd, err := unix.Openat(dirFD, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o400)
	if err != nil {
		return errors.New("candidate stage: immutable artifact already exists or directory is unsafe")
	}
	file := os.NewFile(uintptr(fd), "candidate-manifest")
	if file == nil {
		_ = unix.Close(fd)
		return errors.New("candidate stage: artifact open failed")
	}
	defer file.Close()
	if _, err := file.Write(data); err != nil || unix.Fchmod(fd, 0o400) != nil || file.Sync() != nil || unix.Fsync(dirFD) != nil {
		return errors.New("candidate stage: artifact write failed")
	}
	return nil
}

func runReleaseSigningKeyCheck(args []string) error {
	if len(args) != 0 {
		return errors.New("release attestation: signing key path is accepted only through secure environment/mount")
	}
	key, err := loadReleaseSigningKey()
	if err != nil {
		return err
	}
	defer clear(key)
	publicKey := key.Public().(ed25519.PublicKey)
	if _, err := io.WriteString(os.Stdout, base64.RawStdEncoding.EncodeToString(publicKey)+"\n"); err != nil {
		return errors.New("release attestation: signing public key output failed")
	}
	return nil
}

func runReleaseTrustKeyCheck(args []string) error {
	return runReleaseTrustKeyCheckAt(args, trustRuntimeRoot, os.Getenv("LEADGEN_RELEASE_TRUST_KEY_ID"), readStagedTrustAnchor)
}

func runReleaseTrustKeyCheckAt(args []string, trustDirectory, keyID string, readTrustAnchor func(string, string) (ed25519.PublicKey, error)) error {
	if len(args) != 1 || args[0] != filepath.Join(trustDirectory, "trusted-release.pub") {
		return errors.New("release trust check: public key path rejected")
	}
	if keyID == "" {
		return errors.New("release trust check: trust key ID is required")
	}
	_, err := readTrustAnchor(trustDirectory, keyID)
	if err != nil {
		return errors.New("release trust check: staged trust bundle unavailable or mismatched")
	}
	return nil
}

func loadReleaseSigningKey() (ed25519.PrivateKey, error) {
	keyPath := os.Getenv("LEADGEN_RELEASE_SIGNING_KEY_FILE")
	if keyPath == "" {
		return nil, errors.New("release attestation: signing key file not configured")
	}
	keyFD, err := syscall.Open(keyPath, syscall.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, errors.New("release attestation: signing key file must be regular mode 0600")
	}
	keyFile := os.NewFile(uintptr(keyFD), "release-key")
	defer keyFile.Close()
	var keyInfo syscall.Stat_t
	if err := syscall.Fstat(keyFD, &keyInfo); err != nil || keyInfo.Mode&syscall.S_IFMT != syscall.S_IFREG || keyInfo.Mode&0o777 != 0o600 || int(keyInfo.Uid) != os.Geteuid() {
		return nil, errors.New("release attestation: signing key file must be owner-only regular mode 0600")
	}
	keyData, err := io.ReadAll(io.LimitReader(keyFile, 4096))
	if err != nil {
		return nil, errors.New("release attestation: signing key unreadable")
	}
	defer clear(keyData)
	privateKey, err := parseEd25519PrivateKey(keyData)
	if err != nil {
		return nil, errors.New("release attestation: invalid Ed25519 private key")
	}
	return privateKey, nil
}

func runReleaseVerifyAttestation(args []string) error {
	if len(args) != 1 || args[0] != filepath.Join(trustRuntimeRoot, "trusted-release.pub") {
		return errors.New("release attestation: fixed staged public key path required")
	}
	data, err := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
	if err != nil {
		return errors.New("release attestation: input read failed")
	}
	trustedKeyID := os.Getenv("LEADGEN_RELEASE_TRUST_KEY_ID")
	if trustedKeyID == "" {
		return errors.New("release attestation: LEADGEN_RELEASE_TRUST_KEY_ID is required")
	}
	publicKey, err := readStagedTrustAnchor(trustRuntimeRoot, trustedKeyID)
	if err != nil {
		return errors.New("release attestation: staged trust key unavailable or mismatched")
	}
	_, err = verifyReleaseAttestation(data, publicKey, trustedKeyID,
		strings.Split(os.Getenv("LEADGEN_RELEASE_REVOKED_KEY_IDS"), ","), time.Now().UTC())
	return err
}

func verifyReleaseAttestation(data []byte, publicKey ed25519.PublicKey, trustedKeyID string, revokedKeyIDs []string, now time.Time) (releasePayload, error) {
	return verifyReleaseAttestationMode(data, publicKey, trustedKeyID, revokedKeyIDs, now, false)
}

func verifyReleaseAttestationMode(data []byte, publicKey ed25519.PublicKey, trustedKeyID string, revokedKeyIDs []string, now time.Time, allowExpired bool) (releasePayload, error) {
	var envelope signedAttestation
	if err := strictJSON(data, &envelope); err != nil {
		return releasePayload{}, errors.New("malformed envelope")
	}
	var payload releasePayload
	if err := strictJSON(envelope.Payload, &payload); err != nil {
		return releasePayload{}, errors.New("payload schema rejected")
	}
	canonical, err := canonicalReleasePayload(payload)
	if err != nil || !equalBytes(canonical, envelope.Payload) {
		return releasePayload{}, errors.New("payload canonicalization mismatch")
	}
	signature, err := base64.RawStdEncoding.DecodeString(envelope.Signature)
	if err != nil || !ed25519.Verify(publicKey, canonical, signature) {
		return releasePayload{}, errors.New("signature verification failed")
	}
	if trustedKeyID == "" || trustedKeyID != payload.KeyID {
		return releasePayload{}, errors.New("keyId is not the configured trust key")
	}
	for _, revoked := range revokedKeyIDs {
		if revoked != "" && revoked == payload.KeyID {
			return releasePayload{}, errors.New("signing key is revoked")
		}
	}
	issued, issuedErr := time.Parse(time.RFC3339, payload.IssuedAt)
	expires, expiresErr := time.Parse(time.RFC3339, payload.ExpiresAt)
	if issuedErr != nil || expiresErr != nil || (!allowExpired && !expires.After(now)) || !issued.Before(now.Add(time.Minute)) || !expires.After(issued) {
		return releasePayload{}, errors.New("invalid or expired validity period")
	}
	return payload, nil
}

type releaseExpectation struct {
	DeploymentID            string
	Environment             string
	Target                  string
	DaemonID                string
	LedgerID                string
	Generation              string
	ReleaseNonce            string
	Sequence                uint64
	BaseActive              string
	ImageDigest             string
	BundleFiles             []string
	CandidateManifestSHA256 string
}

func validateReleaseExpectation(payload releasePayload, expected releaseExpectation) error {
	if payload.DeploymentID != expected.DeploymentID || payload.Environment != expected.Environment || payload.Target != expected.Target || payload.DaemonID != expected.DaemonID || payload.LedgerID != expected.LedgerID || payload.Generation != expected.Generation || payload.ReleaseNonce != expected.ReleaseNonce || payload.Sequence != expected.Sequence || payload.BaseActive != expected.BaseActive || payload.ImageDigest != expected.ImageDigest || payload.CandidateManifestSHA256 != expected.CandidateManifestSHA256 || !equalStringSlices(payload.BundleFiles, expected.BundleFiles) {
		return errors.New("attestation does not match expected daemon/deployment/candidate metadata")
	}
	return nil
}

func equalStringSlices(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func canonicalReleasePayload(payload releasePayload) ([]byte, error) {
	if payload.Version != 1 || payload.Purpose != "lead-gen-secret-release" || payload.KeyID == "" || payload.DeploymentID == "" || payload.Environment == "" || payload.Target == "" || payload.DaemonID == "" || payload.LedgerID == "" || payload.Generation == "" || payload.ReleaseNonce == "" || payload.Sequence == 0 || payload.ImageDigest == "" || len(payload.BundleFiles) == 0 {
		return nil, errors.New("required fields missing")
	}
	for _, id := range []string{payload.DeploymentID, payload.Environment, payload.Target, payload.Generation, payload.ReleaseNonce, payload.KeyID} {
		if !safeReleaseID.MatchString(id) {
			return nil, errors.New("invalid identifier")
		}
	}
	if !releaseDigestPattern.MatchString(payload.DaemonID) || !releaseDigestPattern.MatchString(payload.ImageDigest) {
		return nil, errors.New("invalid image/daemon digest")
	}
	if payload.CandidateManifestSHA256 != "" && !releaseDigestPattern.MatchString(payload.CandidateManifestSHA256) {
		return nil, errors.New("invalid candidate manifest digest")
	}
	for i, name := range payload.BundleFiles {
		if name == "" || strings.ContainsAny(name, "/\\") || (i > 0 && payload.BundleFiles[i-1] >= name) {
			return nil, errors.New("bundle files must be sorted unique basenames")
		}
	}
	if err := validateAttestedRuntimeRoles(payload); err != nil {
		return nil, err
	}
	return json.Marshal(payload)
}

func validateAttestedRuntimeRoles(payload releasePayload) error {
	roleTargets := map[string]string{}
	switch payload.Target {
	case "intake":
		roleTargets["request-fulfillment"] = "request-fulfillment"
		roleTargets["consoleauth"] = "consoleauth"
		roleTargets["vector"] = "integration-platform"
		roleTargets["integration-platform"] = "integration-platform-full-runtime"
	case "request-fulfillment":
		roleTargets["request-fulfillment"] = "request-fulfillment"
	case "consoleauth":
		roleTargets["consoleauth"] = "consoleauth"
	case "integration-platform":
		roleTargets["vector"] = "integration-platform"
		roleTargets["integration-platform"] = "integration-platform-full-runtime"
	default:
		if len(payload.RuntimeRoles) != 0 {
			return errors.New("runtime roles are not permitted for this attestation target")
		}
		return nil
	}
	if len(payload.RuntimeRoles) != len(roleTargets) {
		return errors.New("runtime role registry is incomplete")
	}
	for key, target := range roleTargets {
		want, err := migrate.RuntimeRoleName(migrate.RuntimeRoleScope{
			Environment: payload.Environment, Deployment: payload.DeploymentID,
			Target: target, Generation: payload.Generation,
		})
		if err != nil || payload.RuntimeRoles[key] != want {
			return errors.New("runtime role registry does not match signed deployment scope")
		}
	}
	return nil
}

var safeReleaseID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

func strictJSON(data []byte, dst any) error {
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("trailing JSON data")
	}
	return nil
}

func parseEd25519PrivateKey(data []byte) (ed25519.PrivateKey, error) {
	decoded, err := base64.RawStdEncoding.DecodeString(strings.TrimSpace(string(data)))
	if err != nil {
		decoded, err = base64.StdEncoding.DecodeString(strings.TrimSpace(string(data)))
	}
	if err != nil || len(decoded) != ed25519.PrivateKeySize {
		return nil, errors.New("invalid key")
	}
	return ed25519.PrivateKey(decoded), nil
}

func parseEd25519PublicKey(data []byte) (ed25519.PublicKey, error) {
	decoded, err := base64.RawStdEncoding.DecodeString(strings.TrimSpace(string(data)))
	if err != nil {
		decoded, err = base64.StdEncoding.DecodeString(strings.TrimSpace(string(data)))
	}
	if err != nil || len(decoded) != ed25519.PublicKeySize {
		return nil, errors.New("invalid key")
	}
	return ed25519.PublicKey(decoded), nil
}

func releasePayloadDigest(payload json.RawMessage) string {
	digest := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func equalBytes(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
