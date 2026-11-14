// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package docir

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os/exec"
	"syscall"
	"time"
)

// ProcessParser makes the request deadline a hard process-group boundary, even
// when an extraction library is not cooperatively cancellable.
type ProcessParser struct{ Binary string }

func (p ProcessParser) Parse(ctx context.Context, req ParseRequest, source io.Reader) (Result, error) {
	var result Result
	if req.Limits.Validate() != nil {
		return result, ErrLimit
	}
	ctx, cancel := context.WithTimeout(ctx, req.Limits.Deadline)
	defer cancel()
	raw, e := json.Marshal(req)
	if e != nil {
		return result, ErrInvalid
	}
	cmd := exec.CommandContext(ctx, p.Binary, "--stdio")
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8"}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = time.Second
	cmd.Stdin = io.MultiReader(bytes.NewReader(append(raw, '\n')), io.LimitReader(source, req.Limits.MaxSourceBytes+1))
	out := &limitedBuffer{max: req.Limits.MaxDecodedBytes}
	cmd.Stdout = out
	cmd.Stderr = &limitedBuffer{max: 4096}
	if e = cmd.Run(); e != nil {
		for _, known := range []error{ErrOCRUnavailable, ErrLayout, ErrLimit} {
			if bytes.Contains(out.Bytes(), []byte(known.Error())) {
				return result, known
			}
		}
		return result, ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(out.Bytes()))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&result) != nil || decoder.Decode(new(any)) != io.EOF {
		return Result{}, ErrInvalid
	}
	return result, Validate(result, req.Limits)
}
