// SPDX-License-Identifier: MIT

package identifier

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strings"

	"github.com/lennylabs/lenny/scripts/specshift/citation"
)

// site is one occurrence of a retired spelling, given as a byte span of
// the source together with the line it opens on and the spelling it
// carries.
type site struct {
	start   int
	end     int
	line    int
	retired string
	// grpcMethod records that the occurrence stands in the method
	// component of a gRPC full-method literal, which is what selects the
	// proto RPC row over the Go type row when the channel states a
	// spelling in both.
	grpcMethod bool
	// fileName records that the occurrence stands in the name of the
	// tracked file itself, which is the carrier of the path stem and so
	// selects the path row.
	fileName bool
}

// findSites returns every retired-spelling site a text carries, in
// source order, which is the order the register's occurrence numbers are
// assigned in.
//
// Matching is by whole token rather than by substring. The left boundary
// admits an underscore, so the generated `Adapter_AdapterEventsServer`
// stem is one site, and the right boundary admits an uppercase byte, so
// a compound written in camel case is one site as well. A spelling
// standing inside a longer lowercase word is no site.
//
// The spellings are read longest first and a match consumes its span, so
// a stem that is a prefix of a compound does not take the occurrence
// number the compound is registered under.
//
// No continuation join is applied. An identifier is one token that a
// carrier writes on one line, so there is no wrapped spelling for a join
// to fold, and applying one would shift the offsets the substitution is
// spliced at.
func findSites(content string, retired []string) []site {
	var out []site
	for at := 0; at < len(content); {
		match := ""
		for _, spelling := range retired {
			if strings.HasPrefix(content[at:], spelling) && bounded(content, at, at+len(spelling)) {
				match = spelling
				break
			}
		}
		if match == "" {
			at++
			continue
		}
		out = append(out, site{
			start:      at,
			end:        at + len(match),
			line:       citation.LineOf(content, at),
			retired:    match,
			grpcMethod: inGrpcFullMethod(content, at),
		})
		at += len(match)
	}
	return out
}

// bounded reports whether a match stands on token boundaries.
//
// A spelling written in camel case opens a component of the token it
// stands in, so a match beginning with an uppercase byte is bounded on
// the left wherever it sits: the test-function name and the generated
// server-stream stem both carry the channel name after a prefix. A
// spelling written lowercase is bounded on the left by a byte that is
// not alphanumeric, so a stem inside a longer lowercase word is no site.
// On the right, an uppercase byte opens the next component and ends the
// match, and so does an underscore, which is the separator a file name
// and a snake-case token write a component boundary with. A lowercase
// byte or a digit continues the word and rules the match out.
func bounded(content string, lo, hi int) bool {
	if lo > 0 && !isUpper(content[lo]) && isAlphanumeric(content[lo-1]) {
		return false
	}
	return hi >= len(content) || !isLowerWordByte(content[hi])
}

// isUpper reports whether a byte is an uppercase letter, which is what
// opens a camel-case component.
func isUpper(b byte) bool { return b >= 'A' && b <= 'Z' }

// isAlphanumeric reports whether a byte is a letter or a digit. The
// underscore is outside the class, so a generated stem that joins a
// service name to a channel name with one is read as carrying the
// channel name.
func isAlphanumeric(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

// isLowerWordByte reports whether a byte continues the word a match
// ended in. An uppercase byte opens the next component of a camel-case
// compound and an underscore separates two components of a file name or
// a snake-case token, so neither continues the word.
func isLowerWordByte(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'a' && b <= 'z'
}

// inGrpcFullMethod reports whether the site opens the method component
// of a gRPC full-method literal, which is written `/<package>.<Service>/<Method>`.
//
// The form is what selects the proto RPC row. A full-method literal
// carries the RPC name of the proto service definition, and the Go
// method the same channel declares takes a different spelling, so a
// literal resolved from the Go type row would name a method the service
// does not declare while reading as canonical to every gate.
func inGrpcFullMethod(content string, at int) bool {
	if at == 0 || content[at-1] != '/' {
		return false
	}
	service := at - 1
	for service > 0 && isServiceByte(content[service-1]) {
		service--
	}
	if service == 0 || content[service-1] != '/' {
		return false
	}
	return strings.Contains(content[service:at-1], ".")
}

// isServiceByte reports whether a byte can stand in the
// package-qualified service component of a full-method literal.
func isServiceByte(b byte) bool { return isAlphanumeric(b) || b == '.' || b == '_' }

// declaredNameSpans returns the byte span of the name of every
// package-level declaration of a Go carrier, in source order.
//
// It is the position a symbol rename is recorded at. A `::<symbol>`
// reference names a symbol its declaring file declares, and a tier-0
// check resolves it against that file, so a substitution inside a
// declaration's name retires the reference while a substitution in a
// comment, in a string literal, or in a use of a symbol declared
// elsewhere retires nothing. Recording the second kind would key one
// reference to two renames, since the same token stands for one channel
// in a literal and for the other in a declaration.
//
// A file the parser cannot read fails the run rather than being skipped,
// because a skipped file is a declaration whose references are left
// pointing at a symbol the same run renamed.
func declaredNameSpans(target, content string) ([][2]int, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, target, content, parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("read the declarations of %s: %w", target, err)
	}
	var out [][2]int
	add := func(name *ast.Ident) {
		if name == nil {
			return
		}
		lo := fset.Position(name.Pos()).Offset
		out = append(out, [2]int{lo, lo + len(name.Name)})
	}
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			add(d.Name)
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					add(s.Name)
				case *ast.ValueSpec:
					for _, name := range s.Names {
						add(name)
					}
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out, nil
}

// declarationAround returns the span of the declaration name a site
// stands inside, and reports whether it stands inside one.
func declarationAround(spans [][2]int, lo, hi int) ([2]int, bool) {
	for _, span := range spans {
		if lo >= span[0] && hi <= span[1] {
			return span, true
		}
	}
	return [2]int{}, false
}

// edit is one substitution in a file, given as a byte span of the source
// and the text that replaces it.
type edit struct {
	start int
	end   int
	text  string
}

// splice writes every edit into the text. The edits arrive in source
// order and no two sites overlap, because each covers one match of the
// matcher, so the result is assembled in one forward pass.
func splice(text string, edits []edit) string {
	var out strings.Builder
	at := 0
	for _, e := range edits {
		out.WriteString(text[at:e.start])
		out.WriteString(e.text)
		at = e.end
	}
	out.WriteString(text[at:])
	return out.String()
}
