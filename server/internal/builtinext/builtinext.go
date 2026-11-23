// Package builtinext is where a built-in module hands the core what the core needs of it to run the module's
// built-in extension (docs/architecture/core-and-modules.md "Provider seams"): the program a subcommand of personad
// and persona-manager runs (`personad volc`), and what the extension host (internal/extmgr) shows of the extension.
//
// The core declares here what it needs, never a module's types; the composition root (internal/modules/all) provides
// it in the init of the module's file, under the module's build constraint. With the module left out of the build
// nothing is provided: the subcommand is unknown and the extension host registers no such extension, exactly as
// before the extension existed.
//
// Everything is provided while the program initializes, before main runs, and only read afterwards.
package builtinext

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

// Program is a module's built-in extension program, run as a subcommand of the binaries that link the modules.
type Program func(ctx context.Context, version string) error

type program struct {
	run     Program
	summary string
}

var programs = map[string]program{}

// AddProgram provides the program the subcommand name runs and its line in personad's usage (init only).
func AddProgram(name, summary string, run Program) {
	if _, dup := programs[name]; dup {
		panic("builtinext: program " + name + " provided twice")
	}
	programs[name] = program{run, summary}
}

// LookupProgram is the program the subcommand name runs, if a module in this build provides one.
func LookupProgram(name string) (Program, bool) {
	p, ok := programs[name]
	return p.run, ok
}

// ProgramUsage is the usage lines of the programs, by name, as command's subcommands ("" without any).
func ProgramUsage(command string) string {
	names := make([]string, 0, len(programs))
	for name := range programs {
		names = append(names, name)
	}
	slices.Sort(names)
	var b strings.Builder
	for _, name := range names {
		fmt.Fprintf(&b, "  %-26s%s\n", command+" "+name, programs[name].summary)
	}
	return b.String()
}

// Volc is what the extension host shows of the built-in Volcengine speech extension (internal/modules/volc,
// docs/extensions/volcengine-speech.md): its ids and the choices of its settings form.
type Volc struct {
	// ExtensionID is the extension's id; TranscriptionContribution and SpeechContribution its contributions' ids.
	ExtensionID, TranscriptionContribution, SpeechContribution string
	// StatusPage is its page's id, SecretKey the name of its key's secret.
	StatusPage, SecretKey string
	// Voices and ASRModels are the form's choices, the default first.
	Voices, ASRModels []string
}

var volc *Volc

// ProvideVolc provides the Volcengine speech extension (init only).
func ProvideVolc(v Volc) { volc = &v }

// VolcSpeech is the Volcengine speech extension, if its module is in this build.
func VolcSpeech() (Volc, bool) {
	if volc == nil {
		return Volc{}, false
	}
	return *volc, true
}
