# Screen capture backend change

The old hand-written GDI capture path has been removed from the Windows client.
The target now uses `github.com/go-mswin/screencapture` with
`BackendDuplication` (DXGI Desktop Duplication), which exposes native Windows
BGRA frames and keeps row stride explicit.

This deliberately stops changing the viewer's RGB/BGR rendering. The repeated
color patches did not change the observed output, so the capture source is now
replaced rather than another DIB conversion being attempted.

The dependency is CGO-free and requires Go 1.26.4+; the project is already on
Go 1.24.7 in go.mod and the user's Windows toolchain is newer than that.

Run `GET_CAPTURE_DEP.ps1` once from the project root. It fetches the module,
updates go.mod/go.sum, formats the changed files, and builds the Windows GUI.
