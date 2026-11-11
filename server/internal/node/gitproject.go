package node

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/nwverse/persona/server/internal/proto"
)

var nonGitProject = &proto.SessionInventory{ProjectKind: "non_git"}

// gitProject runs only in the existing bounded, asynchronous git-place cache.
// Read fetch URLs from the common config, so a worktree and its main checkout
// agree. Never log command errors: they may contain remote configuration.
func gitProject(metadata string) *proto.SessionInventory {
	real, err := filepath.EvalSymlinks(metadata)
	if err != nil {
		return nil
	}
	config := filepath.Join(real, "config")
	if _, err := os.Stat(config); err == nil {
		out, truncated, err := gitCapped(context.Background(), real, 64<<10,
			"config", "--file", config, "--includes", "--null", "--get-regexp", `^remote\..*\.url$`)
		if err == nil && !truncated {
			if key := configuredRemote(out); key != "" {
				return &proto.SessionInventory{ProjectKind: "remote", ProjectRepo: projectDigest(key)}
			}
		}
	}
	return &proto.SessionInventory{ProjectKind: "local", ProjectRepo: projectDigest(real)}
}

func projectDigest(s string) string {
	digest := sha256.Sum256([]byte(s))
	return hex.EncodeToString(digest[:])
}

func configuredRemote(config string) string {
	var onlyName, onlyURL string
	multiple := false
	for entry := range strings.SplitSeq(config, "\x00") {
		key, value, ok := strings.Cut(entry, "\n")
		if !ok {
			continue
		}
		if key == "remote.origin.url" {
			return normalizedRemote(value)
		}
		if onlyName == "" {
			onlyName, onlyURL = key, value
		} else if key != onlyName {
			multiple = true
		}
	}
	if multiple {
		return ""
	}
	return normalizedRemote(onlyURL)
}

// normalizedRemote deliberately does not resolve aliases or local remotes.
// Keep non-default ports and the complete repository path. HTTPS credentials,
// passwords, query parameters and fragments are not part of the identity.
// Like gitIdentity, preserve a custom SSH account as an opaque principal.
func normalizedRemote(raw string) string {
	raw = strings.TrimSpace(raw)
	var host, port, path, user, namespace string
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil || u.Hostname() == "" || strings.Contains(strings.ToLower(u.EscapedPath()), "%2f") {
			return ""
		}
		host, port, path = u.Hostname(), u.Port(), u.Path
		switch strings.ToLower(u.Scheme) {
		case "https":
			if port == "443" {
				port = ""
			}
		case "http":
			if port == "80" {
				port = ""
			}
		case "ssh", "git+ssh", "ssh+git":
			if port == "22" {
				port = ""
			}
			user = u.User.Username()
			if user == "" {
				return "" // the effective account is local SSH configuration
			}
			if user != "git" {
				namespace = "ssh-absolute:"
			}
		case "git":
			if port == "9418" {
				port = ""
			}
		default:
			return ""
		}
	} else {
		// SCP-style Git syntax: optional login, host, colon, repository path.
		// A slash before the colon is a local path. A drive letter is local too.
		var ok bool
		host, path, ok = strings.Cut(raw, ":")
		if !ok || len(host) <= 1 || strings.ContainsAny(host, `/\\`) || strings.HasPrefix(path, `\`) {
			return ""
		}
		if i := strings.LastIndexByte(host, '@'); i >= 0 {
			user, host = host[:i], host[i+1:]
		}
		if user == "" {
			return "" // a relative path can name different users' home directories
		}
		if strings.HasPrefix(path, "/") {
			namespace = "ssh-absolute:"
		} else if user != "git" {
			namespace = "ssh-home:"
		}
	}
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "" || strings.ContainsAny(host, " /\\?#@\r\n\t") {
		return ""
	}
	path = strings.Trim(path, "/")
	for part := range strings.SplitSeq(path, "/") {
		if part == "" || part == "." || part == ".." || strings.ContainsAny(part, "\\\x00\r\n") {
			return ""
		}
	}
	if port != "" {
		host = net.JoinHostPort(host, port)
	}
	origin := gitNetworkOrigin(host, path, user)
	if origin == "" {
		return ""
	}
	return namespace + origin
}
