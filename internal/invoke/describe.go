package invoke

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/cplieger/deadset/internal/report"
	"github.com/cplieger/deadset/internal/rundir"
)

// The refusals a handshake makes, each carried by a [*HandshakeError].
var (
	// ErrDescribeExited reports a describe verb that exited with a code other
	// than 0, so what it printed is not read.
	ErrDescribeExited = errors.New("describe did not describe the analyzer")

	// ErrDescription reports a describe document the handshake cannot read.
	ErrDescription = errors.New("describe printed no document the handshake reads")

	// ErrNoConformancePass reports an analyzer whose describe document records
	// no conformance record, or a result other than pass.
	ErrNoConformancePass = errors.New("the analyzer records no conformance pass")

	// ErrSchemaVersion reports an analyzer that reads no report schema version
	// the run accepts.
	ErrSchemaVersion = errors.New("the analyzer reads no schema version the run accepts")

	// ErrDescribedName reports an analyzer that describes itself under a name
	// other than its provider entry's.
	ErrDescribedName = errors.New("the analyzer describes itself under another name than its provider entry's")
)

// Description is what an analyzer's describe verb states about it.
type Description struct {
	// Conformance is the analyzer's result over the conformance corpus, or nil
	// where the document states none.
	Conformance *report.Conformance

	Name            string
	Version         string
	ContractVersion string

	// SchemaVersionsAccepted is every report schema version the analyzer reads.
	SchemaVersionsAccepted []string

	// Languages is the languages the analyzer claims.
	Languages []string
}

// HandshakeError is a handshake that admitted no analyzer.
type HandshakeError struct {
	// Err is what went wrong: one of the refusals this package declares for a
	// handshake, the cause of a cancelled run, or the error the analyzer could
	// not be run with or what it printed could not be kept with.
	Err error

	Analyzer string // the provider entry
	Command  string // the executable the provider entry's command resolved to

	// Exit is the describe verb's exit code, or -1 when it did not run or did
	// not exit on its own.
	Exit int
}

// Error renders the refusal as one line naming the entry and its command.
func (e *HandshakeError) Error() string {
	return fmt.Sprintf("analyzer %s, command %s: %v", e.Analyzer, e.Command, e.Err)
}

// Unwrap is the refusal's cause.
func (e *HandshakeError) Unwrap() error { return e.Err }

// Describe runs the describe verb of req.Command before any analysis, keeps
// what it printed at kept's describe file, a command that never started
// keeping nothing, and reads the document. It admits the analyzer, returning
// the description, when the document names the provider entry's name, records
// a conformance pass and reads one of the accepted report schema versions;
// otherwise it returns a [*HandshakeError]. The verb's stderr goes to
// req.Diagnostics, and the scope, configuration and report paths are not read.
func Describe(ctx context.Context, req *Request, kept rundir.Entry, accepted []string) (*Description, error) {
	refuse := func(exit int, err error) error {
		return &HandshakeError{Err: err, Analyzer: req.Analyzer, Command: req.Command, Exit: exit}
	}
	if err := resolved(req.Command); err != nil {
		return nil, refuse(-1, err)
	}

	var printed bytes.Buffer
	cmd := command(ctx, req, req.Command, "describe")
	diagnostics, copied := diagnosticsOf(req.Diagnostics)
	cmd.Stdout = &printed
	cmd.Stderr = diagnostics
	exit, runErr := wait(ctx, cmd, copied)
	if cmd.Process == nil {
		return nil, refuse(exit, runErr)
	}
	keepErr := kept.WriteDescribe(printed.Bytes())
	switch {
	case runErr != nil || keepErr != nil:
		return nil, refuse(exit, errors.Join(runErr, keepErr))
	case exit != 0:
		return nil, refuse(exit, fmt.Errorf("exited %d: %w", exit, ErrDescribeExited))
	}

	described, err := decodeDescription(printed.Bytes())
	if err != nil {
		return nil, refuse(exit, err)
	}
	if described.Name != req.Analyzer {
		return nil, refuse(exit, fmt.Errorf("%w: it names itself %q", ErrDescribedName, described.Name))
	}
	if err := admit(described, accepted); err != nil {
		return nil, refuse(exit, err)
	}
	return described, nil
}

// admit refuses an analyzer whose description records no conformance pass, or
// names no report schema version among accepted.
func admit(described *Description, accepted []string) error {
	switch conformance := described.Conformance; {
	case conformance == nil:
		return fmt.Errorf("%w: describe states no conformance record", ErrNoConformancePass)
	case conformance.Result != report.ResultPass:
		return fmt.Errorf("%w: its result over corpus version %s is %q",
			ErrNoConformancePass, conformance.CorpusVersion, conformance.Result)
	}
	for _, version := range described.SchemaVersionsAccepted {
		if slices.Contains(accepted, version) {
			return nil
		}
	}
	return fmt.Errorf("%w: it reads schema versions %s, and the run accepts %s", ErrSchemaVersion,
		strings.Join(described.SchemaVersionsAccepted, ", "), strings.Join(accepted, ", "))
}

// decodeDescription reads one describe document. Like a report, it is decoded
// with a closed key list: a member the document does not declare (member names
// compare as bytes), a required member that is absent, a member named twice, a
// null, a value of the wrong type, an empty string or array, and anything after
// the object are refused, naming the JSON Pointer of the value at fault.
func decodeDescription(data []byte) (*Description, error) {
	members, err := readObject(data, "",
		[]string{"name", "version", "contract_version", "schema_versions_accepted", "languages"},
		[]string{"conformance"})
	if err != nil {
		return nil, err
	}
	var described Description
	for _, s := range []struct {
		into *string
		name string
	}{
		{&described.Name, "name"},
		{&described.Version, "version"},
		{&described.ContractVersion, "contract_version"},
	} {
		if *s.into, err = stringAt(members[s.name], "/"+s.name); err != nil {
			return nil, err
		}
	}
	if described.SchemaVersionsAccepted, err = stringsAt(members["schema_versions_accepted"], "/schema_versions_accepted"); err != nil {
		return nil, err
	}
	if described.Languages, err = stringsAt(members["languages"], "/languages"); err != nil {
		return nil, err
	}
	if raw, stated := members["conformance"]; stated {
		if described.Conformance, err = conformanceAt(raw, "/conformance"); err != nil {
			return nil, err
		}
	}
	return &described, nil
}

// conformanceAt reads the conformance object raw holds, at the pointer at.
func conformanceAt(raw json.RawMessage, at string) (*report.Conformance, error) {
	members, err := readObject(raw, at, []string{"corpus_version", "result", "digest"}, nil)
	if err != nil {
		return nil, err
	}
	var read [3]string
	for i, name := range []string{"corpus_version", "result", "digest"} {
		if read[i], err = stringAt(members[name], at+"/"+name); err != nil {
			return nil, err
		}
	}
	return &report.Conformance{CorpusVersion: read[0], Result: report.Result(read[1]), Digest: read[2]}, nil
}

// readObject reads the one JSON object data holds as the raw value of each
// member, refusing a member neither required nor optional declares, a member
// named twice, a required member that is absent, and anything after the object.
// at is the object's JSON Pointer. A value is not read here, so a null is
// refused by the reader of the value it stands for.
func readObject(data []byte, at string, required, optional []string) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if open, err := decoder.Token(); err != nil || open != json.Delim('{') {
		return nil, refusal(at, "want an object")
	}
	members := map[string]json.RawMessage{}
	declared := slices.Concat(required, optional)
	for decoder.More() {
		if err := readMember(decoder, at, declared, members); err != nil {
			return nil, err
		}
	}
	if _, err := decoder.Token(); err != nil {
		return nil, refusal(at, "want the object closed: "+err.Error())
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, refusal("", "want one JSON object and nothing after it")
	}
	for _, name := range required {
		if _, stated := members[name]; !stated {
			return nil, refusal(at+"/"+name, "a required member is absent")
		}
	}
	return members, nil
}

// readMember reads the next member of the object decoder is in, at the pointer
// at, into members, refusing a member declared does not name and a member
// members already holds.
func readMember(decoder *json.Decoder, at string, declared []string, members map[string]json.RawMessage) error {
	token, err := decoder.Token()
	if err != nil {
		return refusal(at, err.Error())
	}
	name, _ := token.(string)
	member := at + "/" + strings.NewReplacer("~", "~0", "/", "~1").Replace(name)
	switch _, seen := members[name]; {
	case !slices.Contains(declared, name):
		return refusal(member, "the document declares no such member")
	case seen:
		return refusal(member, "the member is named twice")
	}
	var raw json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		return refusal(member, err.Error())
	}
	members[name] = raw
	return nil
}

// stringAt reads the non-empty string raw holds, at the pointer at.
func stringAt(raw json.RawMessage, at string) (string, error) {
	var read string
	if bytes.Equal(raw, []byte("null")) || json.Unmarshal(raw, &read) != nil {
		return "", refusal(at, "want a string")
	}
	if read == "" {
		return "", refusal(at, "want a non-empty string")
	}
	return read, nil
}

// stringsAt reads the non-empty array of non-empty strings raw holds, at the
// pointer at.
func stringsAt(raw json.RawMessage, at string) ([]string, error) {
	var elements []json.RawMessage
	if bytes.Equal(raw, []byte("null")) || json.Unmarshal(raw, &elements) != nil {
		return nil, refusal(at, "want an array")
	}
	if len(elements) == 0 {
		return nil, refusal(at, "want at least one element")
	}
	read := make([]string, len(elements))
	for i, element := range elements {
		var err error
		if read[i], err = stringAt(element, at+"/"+strconv.Itoa(i)); err != nil {
			return nil, err
		}
	}
	return read, nil
}

// refusal is a describe document refused at the JSON Pointer at for reason.
func refusal(at, reason string) error {
	if at == "" {
		return fmt.Errorf("%w: %s", ErrDescription, reason)
	}
	return fmt.Errorf("%w: %s: %s", ErrDescription, at, reason)
}
