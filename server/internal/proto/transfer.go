package proto

import "time"

// Computer to computer transfers (POST /api/v1/files/transfer, docs/protocol/file-transfer.md): the hub reads the file
// from one machine (file.chunk, pinned to the revision it saw first) and writes it into another's receive folder
// (file.upload.* with dest=inbox) a chunk at a time; nothing is written to the hub's disk.

// FileTransferRequest is the body of POST /api/v1/files/transfer.
type FileTransferRequest struct {
	From FileTransferSource `json:"from"`
	To   FileTransferTarget `json:"to"`
}

type FileTransferSource struct {
	Machine string `json:"machine"`
	Path    string `json:"path"`
	Cwd     string `json:"cwd,omitempty"` // what a relative Path is relative to, as in file/download
}

type FileTransferTarget struct {
	Machine string `json:"machine"`
	Dest    string `json:"dest"` // UploadDestInbox, the only one for now
}

// The states of a transfer: running until every byte is written and the file has its name (done), or until a side
// fails or the client cancels it.
const (
	TransferRunning   = "running"
	TransferDone      = "done"
	TransferFailed    = "failed"
	TransferCancelled = "cancelled"
)

// FileTransfer is one transfer as the hub knows it (the answer of POST and GET, the file_transfer event's transfer).
// It lives in the hub's memory: a hub restart forgets it, and GET then answers 404 (the client sends it again).
type FileTransfer struct {
	ID         string    `json:"id"`
	State      string    `json:"state"`
	From       string    `json:"from"` // the source machine
	To         string    `json:"to"`   // the target machine
	Name       string    `json:"name"` // the file's name on the source
	Bytes      int64     `json:"bytes"`
	Total      int64     `json:"total"`
	Error      string    `json:"error,omitempty"`
	Code       string    `json:"code,omitempty"`        // set with some errors: CodeFileChanged, CodeTooLarge, CodeNodeOutdated…
	ResultPath string    `json:"result_path,omitempty"` // done: the absolute path on the target
	StartedAt  time.Time `json:"started_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// CodeFileChanged: the source file changed while it was being sent (its revision is not the one the transfer began
// with); the client may start it again.
const CodeFileChanged = "file_changed"

// FileTransferEvent is the WebSocket event's type; it goes only to the apps that declared file_transfer.
const FileTransferEvent = "file_transfer"
