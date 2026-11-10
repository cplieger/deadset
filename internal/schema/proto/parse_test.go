package proto

import (
	"errors"
	"slices"
	"testing"
)

// parsed parses src as name and fails the test on any syntax error.
func parsed(t *testing.T, name, src string) *File {
	t.Helper()

	f, err := parseFile(name, []byte(src))
	if err != nil {
		t.Fatalf("parseFile(%s) = error %v, want none", name, err)
	}
	return f
}

// locationAt returns the source location recorded for path, or nil.
func locationAt(f *File, path ...int32) *Location {
	for _, l := range f.Locations {
		if slices.Equal(l.Path, path) {
			return l
		}
	}
	return nil
}

// Every expected value below is what protoc v36.2 writes for the same source
// (`protoc --include_source_info -o`, read back with `--decode`).
func TestParseGroupsAndEditions(t *testing.T) {
	t.Parallel()

	t.Run("a proto2 group is a field and a nested message", func(t *testing.T) {
		t.Parallel()

		f := parsed(t, "order.proto", `syntax = "proto2";
package demo;
message Order {
  optional group Item = 1 {
    optional string sku = 2;
  }
}
`)
		order := f.Messages[0]
		item := order.Fields[0]
		if item.Name != "item" || item.Type != fieldGroup || item.TypeName != "Item" || item.Number != 1 {
			t.Errorf("group field = %q %v %q %d, want item TypeGroup Item 1", item.Name, item.Type, item.TypeName, item.Number)
		}
		if len(order.Nested) != 1 || order.Nested[0].Name != "Item" || order.Nested[0].Fields[0].Name != "sku" {
			t.Errorf("nested messages = %+v, want the group Item holding sku", order.Nested)
		}
		if l := locationAt(f, 4, 0, 3, 0); l == nil || !slices.Equal(l.Span, []int32{3, 2, 5, 3}) {
			t.Errorf("the group message's location = %+v, want span [3 2 5 3]", l)
		}
	})

	t.Run("edition 2024 visibility and an option import", func(t *testing.T) {
		t.Parallel()

		f := parsed(t, "hidden.proto", `edition = "2024";
package demo;
import option "google/protobuf/descriptor.proto";
local message Hidden {
  string name = 1;
}
export enum Tone {
  TONE_UNSPECIFIED = 0;
}
`)
		if f.Syntax != "editions" || f.Edition != "2024" {
			t.Errorf("syntax and edition = %q %q, want editions 2024", f.Syntax, f.Edition)
		}
		if !slices.Equal(f.OptionDeps, []string{"google/protobuf/descriptor.proto"}) || len(f.Deps) != 0 {
			t.Errorf("imports = %v, option imports = %v, want only the option import", f.Deps, f.OptionDeps)
		}
		if got := f.Messages[0].Visibility; got != visibilityLocal {
			t.Errorf("Hidden's visibility = %v, want local", got)
		}
		if got := f.Enums[0].Visibility; got != visibilityExport {
			t.Errorf("Tone's visibility = %v, want export", got)
		}
		if l := locationAt(f, 4, 0); l == nil || !slices.Equal(l.Span, []int32{3, 6, 5, 1}) {
			t.Errorf("Hidden's location = %+v, want span [3 6 5 1], starting at message", l)
		}
	})

	t.Run("an edition 2026 enum value JSON name stays an uninterpreted option", func(t *testing.T) {
		t.Parallel()

		f := parsed(t, "shade.proto", `edition = "2026";
package demo;
import option "google/protobuf/json_enumvalue_options.proto";
enum Shade {
  SHADE_UNSPECIFIED = 0;
  SHADE_DARK = 1 [(pb.enumvalue.json).string = "dark"];
}
`)
		dark := f.Enums[0].Values[1]
		if dark.Name != "SHADE_DARK" || dark.Number != 1 || dark.Options == nil || len(dark.Options.Uninterpreted) != 1 {
			t.Fatalf("SHADE_DARK = %+v, want number 1 with one option", dark)
		}
		option := dark.Options.Uninterpreted[0]
		want := []namePart{{Part: "pb.enumvalue.json", IsExtension: true}, {Part: "string"}}
		if !slices.Equal(option.Name, want) || option.String == nil || *option.String != "dark" {
			t.Errorf("the option = %+v, want (pb.enumvalue.json).string = \"dark\"", option)
		}
	})
}

// Leading, trailing and detached comments attach as protoc's tokenizer
// attaches them, so a comment on a declaration is found at its location.
func TestCommentsAttachAsProtocDoes(t *testing.T) {
	t.Parallel()

	f := parsed(t, "ping.proto", `syntax = "proto3";

// detached

// Leading for Ping.
message Ping { // trailing for Ping
  string id = 1; //deadset:ignore DS1802

  /* block */
  int32 n = 2;
}
`)
	for _, c := range []struct {
		leading, trailing string
		name              string
		path, span        []int32
		detached          []string
	}{
		{name: "Ping", path: []int32{4, 0}, span: []int32{5, 0, 10, 1}, leading: " Leading for Ping.\n", trailing: " trailing for Ping\n", detached: []string{" detached\n"}},
		{name: "id", path: []int32{4, 0, 2, 0}, span: []int32{6, 2, 16}, trailing: "deadset:ignore DS1802\n"},
		{name: "n", path: []int32{4, 0, 2, 1}, span: []int32{9, 2, 14}, leading: " block "},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			l := locationAt(f, c.path...)
			if l == nil {
				t.Fatalf("no location at %v", c.path)
			}
			if !slices.Equal(l.Span, c.span) {
				t.Errorf("span = %v, want %v", l.Span, c.span)
			}
			if l.Leading != c.leading || l.Trailing != c.trailing || !slices.Equal(l.Detached, c.detached) {
				t.Errorf("comments = %q %q %q, want %q %q %q", l.Leading, l.Trailing, l.Detached, c.leading, c.trailing, c.detached)
			}
		})
	}
}

// Each want is protoc v36.2's own error list for the source, in its order.
func TestSyntaxErrorPosition(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name, src string
		want      Errors
	}{
		{
			name: "a tab advances the column to the next multiple of eight",
			src:  "syntax = \"proto3\";\nmessage M {\n\tstring name = ;\n}\n",
			want: Errors{{Line: 3, Column: 23, Message: "Expected field number."}},
		},
		{
			name: "tokenizer and parser errors in the order they are found",
			src:  "syntax = \"proto3\";\nmessage M {\n  string name = 1;\n  int32 count = 08;\n  string label = 2 [default = \"x\n}\n",
			want: Errors{
				{Line: 4, Column: 18, Message: "Numbers starting with leading zero must be in octal."},
				{Line: 4, Column: 17, Message: "Integer out of range."},
				{Line: 5, Column: 33, Message: "Multiline strings are not allowed. Did you miss a \"?."},
				{Line: 6, Column: 1, Message: "Expected \"]\"."},
			},
		},
		{
			name: "a group name is checked at its token",
			src:  "syntax = \"proto2\";\nmessage M {\n  optional group item = 1 {}\n}\n",
			want: Errors{{Line: 3, Column: 18, Message: "Group names must start with a capital letter."}},
		},
		{
			name: "an unknown edition is reported at its string",
			src:  "edition = \"2099\";\n",
			want: Errors{{Line: 1, Column: 11, Message: "Unknown edition \"2099\"."}},
		},
		{
			name: "an unknown syntax is reported at its string",
			src:  "syntax = \"proto4\";\n",
			want: Errors{{Line: 1, Column: 10, Message: "Unrecognized syntax identifier \"proto4\".  This parser only recognizes \"proto2\" and \"proto3\"."}},
		},
		{
			name: "an explicit map_entry is reported at the option name",
			src:  "syntax = \"proto3\";\nmessage M {\n  option map_entry = true;\n  string a = 1;\n}\n",
			want: Errors{{Line: 3, Column: 10, Message: "map_entry should not be set explicitly. Use map<KeyType, ValueType> instead."}},
		},
		{
			name: "allow_alias with no shared number",
			src:  "syntax = \"proto3\";\nenum E {\n  option allow_alias = true;\n  E_A = 0;\n  E_B = 1;\n}\n",
			want: Errors{{Line: 7, Column: 1, Message: "\"E\" declares support for enum aliases but no enum values share field numbers. Please remove the unnecessary 'option allow_alias = true;' declaration."}},
		},
		{
			name: "numbers glued to a decimal point",
			src:  "syntax = \"proto3\";\nmessage M { int32 a = 1.5; int32 b = 0x1.5; string c = 2 [d=x.5]; }\n",
			want: Errors{
				{Line: 2, Column: 23, Message: "Expected field number."},
				{Line: 2, Column: 41, Message: "Hex and octal numbers must be integers."},
				{Line: 2, Column: 41, Message: "Expected \";\"."},
				{Line: 2, Column: 62, Message: "Need space between identifier and decimal point."},
				{Line: 2, Column: 62, Message: "Expected \"]\"."},
			},
		},
		{
			name: "a non-ASCII byte outside a string or comment",
			src:  "syntax = \"proto3\";\nmessage M { int32 a = 1; }\n\xc3\xa9\n",
			want: Errors{
				{Line: 3, Column: 1, Message: "Interpreting non ascii codepoint 195."},
				{Line: 3, Column: 1, Message: "Expected top-level statement (e.g. \"message\")."},
				{Line: 3, Column: 2, Message: "Interpreting non ascii codepoint 169."},
			},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			_, err := parseFile("bad.proto", []byte(c.src))
			var got Errors
			if !errors.As(err, &got) {
				t.Fatalf("parseFile() = error %v, want Errors", err)
			}
			if !slices.Equal(got, c.want) {
				t.Errorf("parseFile() errors =\n%v\nwant\n%v", got, c.want)
			}
		})
	}
}
