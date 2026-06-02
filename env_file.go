package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// envFile is an in-place editor for docker-compose-style .env files. The
// parser preserves comments + blank lines + ordering so when we rewrite
// the file after an edit it's still grep-able and roughly diff-friendly.

type envLine struct {
	raw     string // verbatim line content (without trailing \n)
	key     string // populated for KEY=VALUE lines, empty for comments/blanks
	rawKV   bool   // true iff this is a key=value assignment
	quoted  bool   // value was double-quoted in the source
}

type envFile struct {
	path  string
	lines []envLine
}

func readEnvFile(path string) (*envFile, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	out := &envFile{path: path}
	scanner := bufio.NewScanner(f)
	// .env files can have long lines if secrets are base64 — bump the buffer.
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") {
			out.lines = append(out.lines, envLine{raw: line})
			continue
		}
		k, v, ok := strings.Cut(trim, "=")
		if !ok {
			out.lines = append(out.lines, envLine{raw: line})
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		quoted := false
		if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
			quoted = true
			v = v[1 : len(v)-1]
		}
		_ = v // value is recoverable from raw; we just need to know the key
		out.lines = append(out.lines, envLine{
			raw:    line,
			key:    k,
			rawKV:  true,
			quoted: quoted,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (e *envFile) get(key string) (string, bool) {
	for _, l := range e.lines {
		if !l.rawKV || l.key != key {
			continue
		}
		_, v, _ := strings.Cut(l.raw, "=")
		v = strings.TrimSpace(v)
		if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
			v = v[1 : len(v)-1]
		}
		return v, true
	}
	return "", false
}

// set replaces the value of key in place. Returns false if the key was
// not found (callers can decide whether to append).
func (e *envFile) set(key, value string) bool {
	for i, l := range e.lines {
		if !l.rawKV || l.key != key {
			continue
		}
		quoted := l.quoted
		// Re-quote when the new value contains a space or starts/ends with
		// whitespace, even if the old one wasn't quoted; otherwise `source
		// .env` would word-split.
		if strings.ContainsAny(value, " \t") {
			quoted = true
		}
		var rendered string
		if quoted {
			rendered = fmt.Sprintf("%s=\"%s\"", key, value)
		} else {
			rendered = fmt.Sprintf("%s=%s", key, value)
		}
		e.lines[i].raw = rendered
		e.lines[i].quoted = quoted
		return true
	}
	return false
}

// save writes the file atomically via a tmp+rename, preserving any
// existing mode bits on the destination.
func (e *envFile) save() error {
	mode := os.FileMode(0o600)
	if info, err := os.Stat(e.path); err == nil {
		mode = info.Mode().Perm()
	}
	tmp := e.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	for _, l := range e.lines {
		w.WriteString(l.raw)
		w.WriteByte('\n')
	}
	if err := w.Flush(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, e.path)
}
