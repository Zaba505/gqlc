package doc

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"github.com/gqlc/gqlc/gen"
	"github.com/gqlc/gqlc/sort"
	"github.com/gqlc/graphql/ast"
	"github.com/gqlc/graphql/parser"
	"github.com/gqlc/graphql/token"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var (
	update = flag.Bool("update", false, "Update expected output file")

	// Flags are used here to allow for the input/output files to be changed during dev
	// One use case of changing the files is to examine how Generate scales through the benchmark
	//
	gqlFileName   = flag.String("gqlFile", "test.gql", "Specify a .gql file to use a input for testing.")
	exMdDocName   = flag.String("expectedMdFile", "test.md", "Specify a .md file which is the expected generator output from the given .gql file.")
	exHtmlDocName = flag.String("expectedHTMLFile", "test.html", "Specify a .html file which is the expected generator output from the given .gql file.")

	testDoc            *ast.Document
	exMdDoc, exHtmlDoc io.Reader
)

func TestMain(m *testing.M) {
	// Get current working directory
	wd, err := os.Getwd()
	if err != nil {
		panic(err)
	}

	// Parse flags
	flag.Parse()

	// Assume the input file is in the current working directory
	if !filepath.IsAbs(*gqlFileName) {
		*gqlFileName = filepath.Join(wd, *gqlFileName)
	}
	f, err := os.Open(*gqlFileName)
	if err != nil {
		panic(err)
	}

	// Assume the .md output file is in the current working directory
	if !filepath.IsAbs(*exMdDocName) {
		*exMdDocName = filepath.Join(wd, *exMdDocName)
	}
	exMdDoc, err = os.Open(*exMdDocName)
	if err != nil {
		panic(err)
	}

	// Assume the .html output file is in the current working directory
	if !filepath.IsAbs(*exHtmlDocName) {
		*exHtmlDocName = filepath.Join(wd, *exHtmlDocName)
	}
	exHtmlDoc, err = os.Open(*exHtmlDocName)
	if err != nil {
		panic(err)
	}

	testDoc, err = parser.ParseDoc(token.NewDocSet(), "test", f, 0)
	if err != nil {
		panic(err)
	}

	os.Exit(m.Run())
}

func TestUpdate(t *testing.T) {
	if !*update {
		t.Skipf("not updating expected output files: %s,%s", *exMdDocName, *exHtmlDocName)
		return
	}
	t.Run(".md", func(subT *testing.T) {
		t.Logf("updating expected md output file: %s", *exMdDocName)

		f, err := os.OpenFile(*exMdDocName, os.O_RDWR|os.O_CREATE, 0755)
		if err != nil {
			t.Error(err)
			return
		}

		g := new(Generator)
		ctx := gen.WithContext(context.Background(), gen.TestCtx{Writer: f})
		err = g.Generate(ctx, testDoc, "")
		if err != nil {
			t.Error(err)
			return
		}
	})

	t.Run(".html", func(subT *testing.T) {
		t.Logf("updating expected html output file: %s", *exHtmlDocName)

		f, err := os.OpenFile(*exHtmlDocName, os.O_RDWR|os.O_CREATE, 0755)
		if err != nil {
			t.Error(err)
			return
		}

		g := new(Generator)
		ctx := gen.WithContext(context.Background(), gen.TestCtx{Writer: f})
		err = g.Generate(ctx, testDoc, `{"html": true}`)
		if err != nil {
			t.Error(err)
			return
		}
	})
}

func TestAddContent(t *testing.T) {
	mask := sort.SchemaType | sort.ScalarType | sort.ObjectType | sort.InterType | sort.UnionType | sort.EnumType | sort.InputType | sort.DirectiveType | sort.ExtendType

	testCases := []struct {
		Name string
		C    []struct {
			name  string
			count int
			typ   sort.DeclType
		}
		Total int
	}{
		{
			Name: "SingleType",
			C: []struct {
				name  string
				count int
				typ   sort.DeclType
			}{
				{
					name: "Test",
					typ:  sort.ScalarType,
				},
			},
			Total: 2,
		},
		{
			Name: "MultiSameType",
			C: []struct {
				name  string
				count int
				typ   sort.DeclType
			}{
				{
					name: "A",
					typ:  sort.ScalarType,
				},
				{
					name: "B",
					typ:  sort.ScalarType,
				},
				{
					name: "C",
					typ:  sort.ScalarType,
				},
			},
			Total: 4,
		},
		{
			Name: "ManyTypes",
			C: []struct {
				name  string
				count int
				typ   sort.DeclType
			}{
				{
					name: "A",
					typ:  sort.ScalarType,
				},
				{
					name: "B",
					typ:  sort.ScalarType,
				},
				{
					name: "C",
					typ:  sort.ScalarType,
				},
				{
					name: "A",
					typ:  sort.ObjectType,
				},
				{
					name: "B",
					typ:  sort.ObjectType,
				},
				{
					name: "C",
					typ:  sort.ObjectType,
				},
			},
			Total: 8,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.Name, func(subT *testing.T) {
			toc := make([]struct {
				name  string
				count int
			}, 0, len(testCase.C))
			opts := &Options{
				toc: &toc,
			}

			tmask := mask
			for _, c := range testCase.C {
				tmask = opts.addContent(c.name, c.count, c.typ, tmask)
			}

			if len(*opts.toc) != testCase.Total {
				fmt.Println(*opts.toc)
				subT.Fail()
			}
		})
	}
}

func TestToC(t *testing.T) {
	testCases := []struct {
		Name string
		ToC  []struct {
			name  string
			count int
		}
		Ex []byte
	}{
		{
			Name: "SingleSection",
			ToC: []struct {
				name  string
				count int
			}{
				{
					name: scalar,
				},
				{
					name: "Int",
				},
				{
					name: "Float",
				},
				{
					name: "String",
				},
			},
			Ex: []byte(`# Test
*This was generated by gqlc.*

## Table of Contents
- [Scalars](#Scalars)
	* [Int](#Int)
	* [Float](#Float)
	* [String](#String)

`),
		},
		{
			Name: "MultipleSections",
			ToC: []struct {
				name  string
				count int
			}{
				{
					name: scalar,
				},
				{
					name: "Int",
				},
				{
					name: "Float",
				},
				{
					name: "String",
				},
				{
					name: object,
				},
				{
					name: "Person",
				},
				{
					name: "Hero",
				},
				{
					name: "Jedi",
				},
				{
					name: inter,
				},
				{
					name: "Node",
				},
				{
					name: "Connection",
				},
			},
			Ex: []byte(`# Test
*This was generated by gqlc.*

## Table of Contents
- [Scalars](#Scalars)
	* [Int](#Int)
	* [Float](#Float)
	* [String](#String)
- [Objects](#Objects)
	* [Person](#Person)
	* [Hero](#Hero)
	* [Jedi](#Jedi)
- [Interfaces](#Interfaces)
	* [Node](#Node)
	* [Connection](#Connection)

`),
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.Name, func(subT *testing.T) {
			opts := &Options{
				Title: "Test",
				toc:   &testCase.ToC,
			}

			var b bytes.Buffer
			writeToC(&b, opts)
			gen.CompareBytes(subT, testCase.Ex, b.Bytes())
		})
	}
}

func TestFields(t *testing.T) {
	g := new(Generator)

	testCases := []struct {
		Name   string
		Fields []*ast.Field
		Ex     []byte
	}{
		{
			Name: "JustFields",
			Fields: []*ast.Field{
				{
					Name: &ast.Ident{Name: "one"},
					Type: &ast.Field_Ident{Ident: &ast.Ident{Name: "Int"}},
				},
				{
					Name: &ast.Ident{Name: "str"},
					Type: &ast.Field_Ident{Ident: &ast.Ident{Name: "String"}},
				},
				{
					Name: &ast.Ident{Name: "list"},
					Type: &ast.Field_List{List: &ast.List{Type: &ast.List_Ident{Ident: &ast.Ident{Name: "Test"}}}},
				},
			},
			Ex: []byte(`- one **(Int)**
- str **(String)**
- list **([[Test](#Test)])**
`),
		},
		{
			Name: "WithDescriptions",
			Fields: []*ast.Field{
				{
					Name: &ast.Ident{Name: "one"},
					Doc: &ast.DocGroup{
						List: []*ast.DocGroup_Doc{
							{Text: "one is a Int."},
						},
					},
					Type: &ast.Field_Ident{Ident: &ast.Ident{Name: "Int"}},
				},
				{
					Name: &ast.Ident{Name: "str"},
					Doc: &ast.DocGroup{
						List: []*ast.DocGroup_Doc{
							{Text: "str is a String."},
						},
					},
					Type: &ast.Field_Ident{Ident: &ast.Ident{Name: "String"}},
				},
				{
					Name: &ast.Ident{Name: "list"},
					Doc: &ast.DocGroup{
						List: []*ast.DocGroup_Doc{
							{Text: "list is a List."},
						},
					},
					Type: &ast.Field_List{List: &ast.List{Type: &ast.List_Ident{Ident: &ast.Ident{Name: "Test"}}}},
				},
			},
			Ex: []byte(`- one **(Int)**

	one is a Int.
- str **(String)**

	str is a String.
- list **([[Test](#Test)])**

	list is a List.
`),
		},
		{
			Name: "WithArgs",
			Fields: []*ast.Field{
				{
					Name: &ast.Ident{Name: "one"},
					Type: &ast.Field_Ident{Ident: &ast.Ident{Name: "Int"}},
					Args: &ast.InputValueList{
						List: []*ast.InputValue{
							{
								Name: &ast.Ident{Name: "toNumber"},
								Type: &ast.InputValue_Ident{Ident: &ast.Ident{Name: "String"}},
							},
						},
					},
				},
				{
					Name: &ast.Ident{Name: "str"},
					Type: &ast.Field_Ident{Ident: &ast.Ident{Name: "String"}},
					Args: &ast.InputValueList{
						List: []*ast.InputValue{
							{
								Name: &ast.Ident{Name: "toString"},
								Type: &ast.InputValue_Ident{Ident: &ast.Ident{Name: "Int"}},
							},
						},
					},
				},
				{
					Name: &ast.Ident{Name: "list"},
					Type: &ast.Field_List{List: &ast.List{Type: &ast.List_Ident{Ident: &ast.Ident{Name: "Test"}}}},
					Args: &ast.InputValueList{
						List: []*ast.InputValue{
							{
								Name: &ast.Ident{Name: "first"},
								Type: &ast.InputValue_Ident{Ident: &ast.Ident{Name: "Int"}},
							},
							{
								Name: &ast.Ident{Name: "after"},
								Type: &ast.InputValue_Ident{Ident: &ast.Ident{Name: "String"}},
							},
						},
					},
				},
			},
			Ex: []byte(`- one **(Int)**

	*Args*:
	- toNumber **(String)**
- str **(String)**

	*Args*:
	- toString **(Int)**
- list **([[Test](#Test)])**

	*Args*:
	- first **(Int)**
	- after **(String)**
`),
		},
	}

	var testBuf bytes.Buffer
	for _, testCase := range testCases {
		t.Run(testCase.Name, func(subT *testing.T) {
			g.Lock()
			defer g.Unlock()
			g.Reset()

			g.generateFields(testCase.Fields, &testBuf)

			gen.CompareBytes(subT, testCase.Ex, g.Bytes())
		})
	}
}

func TestArgs(t *testing.T) {
	g := new(Generator)

	testCases := []struct {
		Name string
		Args []*ast.InputValue
		Ex   []byte
	}{
		{
			Name: "WithDefaults",
			Args: []*ast.InputValue{
				{
					Name:    &ast.Ident{Name: "one"},
					Type:    &ast.InputValue_Ident{Ident: &ast.Ident{Name: "Int"}},
					Default: &ast.InputValue_BasicLit{BasicLit: &ast.BasicLit{Kind: token.Token_INT, Value: "1"}},
				},
				{
					Name:    &ast.Ident{Name: "str"},
					Type:    &ast.InputValue_Ident{Ident: &ast.Ident{Name: "String"}},
					Default: &ast.InputValue_BasicLit{BasicLit: &ast.BasicLit{Kind: token.Token_STRING, Value: "\"hello\""}},
				},
				{
					Name: &ast.Ident{Name: "list"},
					Type: &ast.InputValue_List{List: &ast.List{Type: &ast.List_Ident{Ident: &ast.Ident{Name: "Test"}}}},
					Default: &ast.InputValue_CompositeLit{CompositeLit: &ast.CompositeLit{
						Value: &ast.CompositeLit_ListLit{ListLit: &ast.ListLit{
							List: &ast.ListLit_BasicList{
								BasicList: &ast.ListLit_Basic{
									Values: []*ast.BasicLit{
										{Value: "1"},
										{Value: "2"},
										{Value: "3"},
									},
								},
							},
						}},
					}},
				},
			},
			Ex: []byte("- one **(Int)**\n\n" +
				"	*Default Value*: `1`\n" +
				"- str **(String)**\n\n" +
				"	*Default Value*: `\"hello\"`\n" +
				"- list **([[Test](#Test)])**\n\n" +
				"	*Default Value*: `[1, 2, 3]`\n"),
		},
	}

	var testBuf bytes.Buffer
	for _, testCase := range testCases {
		t.Run(testCase.Name, func(subT *testing.T) {
			g.Lock()
			defer g.Unlock()
			g.Reset()

			g.generateArgs(testCase.Args, &testBuf)

			gen.CompareBytes(subT, testCase.Ex, g.Bytes())
		})
	}
}

func TestGenerator_Generate(t *testing.T) {
	t.Run("Markdown", func(subT *testing.T) {
		var b bytes.Buffer
		g := new(Generator)
		ctx := gen.WithContext(context.Background(), gen.TestCtx{Writer: &b})
		err := g.Generate(ctx, testDoc, "")
		if err != nil {
			subT.Error(err)
			return
		}

		// Compare generated output to golden output
		ex, err := ioutil.ReadAll(exMdDoc)
		if err != nil {
			subT.Error(err)
			return
		}

		gen.CompareBytes(subT, ex, b.Bytes())
	})

	t.Run("WithHTML", func(subT *testing.T) {
		var b bytes.Buffer
		g := new(Generator)
		ctx := gen.WithContext(context.Background(), gen.TestCtx{Writer: &b})
		err := g.Generate(ctx, testDoc, `{"html": true}`)
		if err != nil {
			subT.Error(err)
			return
		}

		// Compare generated output to golden output
		ex, err := ioutil.ReadAll(exHtmlDoc)
		if err != nil {
			subT.Error(err)
			return
		}

		gen.CompareBytes(subT, ex, b.Bytes())
	})
}

func BenchmarkGenerator_Generate(b *testing.B) {
	var buf bytes.Buffer
	g := new(Generator)
	ctx := gen.WithContext(context.Background(), gen.TestCtx{Writer: &buf})

	for i := 0; i < b.N; i++ {
		buf.Reset()

		err := g.Generate(ctx, testDoc, "")
		if err != nil {
			b.Error(err)
			return
		}
	}
}

func ExampleGenerator_Generate() {
	g := new(Generator)

	gqlSrc := `schema {
	query: Query
}

"Query represents the queries this example provides."
type Query {
	hello: String
}`

	doc, err := parser.ParseDoc(token.NewDocSet(), "example", strings.NewReader(gqlSrc), 0)
	if err != nil {
		return // Handle error
	}

	var b bytes.Buffer
	ctx := gen.WithContext(context.Background(), gen.TestCtx{Writer: &b}) // Pass in an actual
	err = g.Generate(ctx, doc, `{"title": "Example Documentation"}`)
	if err != nil {
		return // Handle err
	}

	fmt.Println(b.String())

	// Output:
	// # Example Documentation
	// *This was generated by gqlc.*
	//
	// ## Table of Contents
	// - [Schema](#Schema)
	// - [Objects](#Objects)
	// 	* [Query](#Query)
	//
	// ## Schema
	//
	// *Root Operations*:
	// - query **([Query](#Query))**
	//
	// ## Objects
	//
	// ### Query
	// Query represents the queries this example provides.
	//
	// *Fields*:
	// - hello **(String)**
}
