// Package js contains a Javascript generator for GraphQL Documents.
package js

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/gqlc/gqlc/gen"
	"github.com/gqlc/graphql/ast"
	"github.com/gqlc/graphql/token"
	"go.uber.org/zap"
)

const (
	// Types
	schemaBit uint16 = 1 << iota
	scalarBit
	objectBit
	interfaceBit
	unionBit
	enumBit
	inputObjectBit
	directiveBit

	// Wrapping Types
	listBit
	nonNullBit

	// Builtin Scalar Types
	intBit
	floatBit
	stringBit
	booleanBit
	idBit
)

// Options contains the options for the JavaScript generator.
type Options struct {
	// Either "COMMONJS" of "ES6"
	Module string

	// Add @flow comment
	UseFlow bool

	// Copy descriptions to Javascript
	Descriptions bool

	imports [][]byte
	declStr []byte
}

var bits = []struct {
	bit uint16
	imp []byte
}{
	{bit: schemaBit, imp: []byte("GraphQLSchema")},
	{bit: scalarBit, imp: []byte("GraphQLScalarType")},
	{bit: objectBit, imp: []byte("GraphQLObjectType")},
	{bit: interfaceBit, imp: []byte("GraphQLInterfaceType")},
	{bit: unionBit, imp: []byte("GraphQLUnionType")},
	{bit: enumBit, imp: []byte("GraphQLEnumType")},
	{bit: inputObjectBit, imp: []byte("GraphQLInputObjectType")},
	{bit: directiveBit, imp: []byte("GraphQLDirectiveType")},
	{bit: listBit, imp: []byte("GraphQLList")},
	{bit: nonNullBit, imp: []byte("GraphQLNonNull")},
	{bit: intBit, imp: []byte("GraphQLInt")},
	{bit: floatBit, imp: []byte("GraphQLFloat")},
	{bit: stringBit, imp: []byte("GraphQLString")},
	{bit: booleanBit, imp: []byte("GraphQLBoolean")},
	{bit: idBit, imp: []byte("GraphQLID")},
}

func (o *Options) setImports(mask uint16) {
	for _, p := range bits {
		if mask&p.bit == 0 {
			o.imports = append(o.imports, p.imp)
		}
	}
}

// Generator generates Javascript code for a GraphQL schema.
type Generator struct {
	sync.Mutex
	bytes.Buffer

	indent []byte
	log    *zap.Logger
}

// Reset overrides the bytes.Buffer Reset method to assist in cleaning up some Generator state.
func (g *Generator) Reset() {
	g.Buffer.Reset()
	if g.indent == nil {
		g.indent = make([]byte, 0, 10)
	}
	g.indent = g.indent[0:0]
}

// Generate generates Javascript code for the given document.
func (g *Generator) Generate(ctx context.Context, doc *ast.Document, opts map[string]interface{}) (err error) {
	g.Lock()
	defer func() {
		if err != nil {
			err = gen.GeneratorError{
				DocName: doc.Name,
				GenName: "js",
				Msg:     err.Error(),
			}
		}
	}()
	defer g.Unlock()
	g.Reset()

	if g.log == nil {
		g.log = zap.L().Named("js").With(zap.String("doc", doc.Name))
	}

	// Get generator options
	g.log.Info("getting options")
	gOpts, oerr := getOptions(doc, opts)
	if oerr != nil {
		return oerr
	}

	// Create bit mask for tracking imports
	mask := schemaBit | scalarBit | objectBit | interfaceBit | unionBit | enumBit | inputObjectBit | directiveBit
	mask |= listBit | nonNullBit
	mask |= intBit | floatBit | stringBit | booleanBit | idBit

	// Generate schema
	if doc.Schema != nil {
		g.log.Info("generating schema")
		mask &= ^schemaBit
		g.generateSchema(gOpts, doc.Schema.Spec.(*ast.TypeDecl_TypeSpec).TypeSpec)
		g.P()
	}

	// Generate types
	g.log.Info("generating types")
	totalTypes := len(doc.Types) - 1
	for i, d := range doc.Types {
		ts, ok := d.Spec.(*ast.TypeDecl_TypeSpec)
		if !ok {
			continue
		}
		if _, ok = ts.TypeSpec.Type.(*ast.TypeSpec_Schema); ok {
			continue
		}

		// Generate variable declaration
		name := ts.TypeSpec.Name.Name
		g.Write(gOpts.declStr)
		g.WriteByte(' ')
		g.WriteString(name)
		g.WriteString("Type")
		g.WriteByte(' ')
		g.WriteByte('=')
		g.WriteByte(' ')
		g.WriteString("new")
		g.WriteByte(' ')

		// Generate GraphQL*Type construction
		switch ts.TypeSpec.Type.(type) {
		case *ast.TypeSpec_Scalar:
			g.generateScalar(&mask, name, gOpts.Descriptions, d.Doc, ts.TypeSpec)

			mask &= ^scalarBit
		case *ast.TypeSpec_Object:
			g.generateObject(&mask, name, gOpts.Descriptions, d.Doc, ts.TypeSpec)

			mask &= ^objectBit
		case *ast.TypeSpec_Interface:
			g.generateInterface(&mask, name, gOpts.Descriptions, d.Doc, ts.TypeSpec)

			mask &= ^interfaceBit
		case *ast.TypeSpec_Union:
			g.generateUnion(&mask, name, gOpts.Descriptions, d.Doc, ts.TypeSpec)

			mask &= ^unionBit
		case *ast.TypeSpec_Enum:
			g.generateEnum(&mask, name, gOpts.Descriptions, d.Doc, ts.TypeSpec)

			mask &= ^enumBit
		case *ast.TypeSpec_Input:
			g.generateInput(&mask, name, gOpts.Descriptions, d.Doc, ts.TypeSpec)

			mask &= ^inputObjectBit
		case *ast.TypeSpec_Directive:
			g.generateDirective(&mask, name, gOpts.Descriptions, d.Doc, ts.TypeSpec)

			mask &= ^directiveBit
		}

		if i != totalTypes {
			g.P()
		}
	}

	// Extract generator context
	gCtx := gen.Context(ctx)

	// Open file to write to
	jsFileName := doc.Name[:len(doc.Name)-len(filepath.Ext(doc.Name))]
	jsFile, err := gCtx.Open(jsFileName + ".js")
	defer jsFile.Close()
	if err != nil {
		return
	}

	// Write module import statement
	g.log.Info("writing module import statement")
	gOpts.setImports(mask)
	_, err = g.writeImports(jsFile, gOpts)
	if err != nil {
		return
	}

	// Write generated output
	_, err = g.WriteTo(jsFile)
	return
}

var (
	flowDirective = []byte("// @flow")

	commonJSDecl   = []byte("var")
	commonJSImport = []byte("= require('graphql');")

	es6Decl       = []byte("let")
	es6ImportDecl = []byte("import")
	es6Import     = []byte("from 'graphql';")

	indent = []byte{' ', ' '}
)

// writeImports writes the module import statement to the given io.Writer.
func (g *Generator) writeImports(w io.Writer, opts *Options) (int, error) {
	var b bytes.Buffer
	b.Grow(350)

	if opts.UseFlow {
		b.Write(flowDirective)
		b.WriteByte('\n')
		b.WriteByte('\n')
	}

	declStr := commonJSDecl
	if opts.Module == "ES6" {
		declStr = es6ImportDecl
	}
	b.Write(declStr)
	b.WriteByte(' ')
	b.WriteByte('{')

	indent := indent
	impLen := len(opts.imports)
	if impLen > 1 {
		b.WriteByte('\n')
	} else {
		indent = indent[:1]
	}

	for i, imp := range opts.imports {
		b.Write(indent)
		b.Write(imp)
		if i != impLen-1 {
			b.WriteByte(',')
			b.WriteByte('\n')
		}
	}
	if impLen == 1 {
		b.WriteByte(' ')
	} else {
		b.WriteByte('\n')
	}
	b.WriteString("} ")

	switch opts.Module {
	case "COMMONJS":
		b.Write(commonJSImport)
	case "ES6":
		b.Write(es6Import)
	}
	b.WriteByte('\n')
	b.WriteByte('\n')

	return w.Write(b.Bytes())
}

func (g *Generator) generateSchema(opts *Options, ts *ast.TypeSpec) {
	schema := ts.Type.(*ast.TypeSpec_Schema).Schema

	var query, mutation *ast.Field
	for _, f := range schema.RootOps.List {
		lname := strings.ToLower(f.Name.Name)
		if lname == "query" {
			query = f
		}
		if lname == "mutation" {
			mutation = f
		}
	}

	g.P(opts.declStr, " Schema = new GraphQLSchema({")
	g.In()

	g.Write(g.indent)
	g.WriteString("query: " + query.Type.(*ast.Field_Ident).Ident.Name)

	if mutation != nil {
		g.WriteByte(',')
		g.WriteByte('\n')
		g.Write(g.indent)
		g.WriteString("mutation: " + mutation.Type.(*ast.Field_Ident).Ident.Name)
	}

	g.Out()
	g.P()
	g.P("});")
}

func (g *Generator) generateScalar(imports *uint16, name string, descr bool, doc *ast.DocGroup, ts *ast.TypeSpec) {
	g.P("GraphQLScalarType({")
	g.In()
	g.P("name: '", name, "',")

	if doc != nil && descr {
		text := doc.Text()
		if len(text) > 0 {
			g.P("description: '", text[:len(text)-1], "',")
		}
	}

	g.P("serialize(value) { /* TODO */ }")
	g.Out()

	g.P("});")
}

func (g *Generator) generateObject(imports *uint16, name string, descr bool, doc *ast.DocGroup, ts *ast.TypeSpec) {
	obj := ts.Type.(*ast.TypeSpec_Object).Object

	g.P("GraphQLObjectType({")
	g.In()

	g.P("name: '", name, "',")

	// Print interfaces
	interLen := len(obj.Interfaces)
	if interLen == 1 {
		g.P("interfaces: [ ", obj.Interfaces[0].Name, " ],")
	}
	if interLen > 1 {
		g.P("interfaces: [")
		g.In()

		str := []interface{}{"", ","}
		for i, inter := range obj.Interfaces {
			str[0] = inter.Name
			if i == interLen-1 {
				str[1] = ""
			}
			g.P(str...)
		}

		g.Out()
		g.P("],")
	}

	g.P("fields: {")
	g.In()

	g.generateFields(obj.Fields, imports, descr, true)

	g.Out()

	g.Write(g.indent)
	g.WriteByte('}')

	if doc != nil && descr {
		g.printDescr(doc)
	}

	g.WriteByte('\n')

	g.Out()
	g.P("});")
}

func (g *Generator) generateInterface(imports *uint16, name string, descr bool, doc *ast.DocGroup, ts *ast.TypeSpec) {
	inter := ts.Type.(*ast.TypeSpec_Interface).Interface

	g.P("GraphQLInterfaceType({")
	g.In()

	g.P("name: '", name, "',")

	g.P("fields: {")
	g.In()

	g.generateFields(inter.Fields, imports, descr, false)

	g.Out()

	g.Write(g.indent)
	g.WriteByte('}')

	if doc != nil && descr {
		g.printDescr(doc)
	}

	g.WriteByte('\n')

	g.Out()
	g.P("});")
}

func (g *Generator) generateFields(fields *ast.FieldList, imports *uint16, descr, resolve bool) {
	fLen := len(fields.List)
	for i, f := range fields.List {
		g.P(f.Name.Name, ": {")
		g.In()

		g.Write(g.indent)
		g.WriteString("type: ")

		var fieldType interface{}
		switch v := f.Type.(type) {
		case *ast.Field_Ident:
			fieldType = v.Ident
		case *ast.Field_List:
			fieldType = v.List
		case *ast.Field_NonNull:
			fieldType = v.NonNull
		}
		g.printType(imports, fieldType)

		if f.Args != nil {
			g.WriteByte(',')
			g.WriteByte('\n')

			g.P("args: {")
			g.In()

			g.generateArgs(f.Args.List, imports, descr)

			g.Out()
			g.Write(g.indent)
			g.WriteByte('}')
		}

		if resolve {
			g.WriteByte(',')
			g.WriteByte('\n')

			g.Write(g.indent)
			g.WriteString("resolve() { /* TODO */ }")
		}

		if descr {
			g.printDescr(f.Doc)
		}

		g.WriteByte('\n')

		g.Out()
		g.Write(g.indent)
		g.WriteByte('}')
		if i != fLen-1 {
			g.WriteByte(',')
		}
		g.WriteByte('\n')
	}
}

func (g *Generator) generateUnion(imports *uint16, name string, descr bool, doc *ast.DocGroup, ts *ast.TypeSpec) {
	union := ts.Type.(*ast.TypeSpec_Union).Union

	g.P("GraphQLUnionType({")
	g.In()

	g.P("name: '", name, "',")

	// Print members
	memsLen := len(union.Members)
	if memsLen == 1 {
		g.P("types: [ ", union.Members[0], " ],")
	}
	if memsLen > 1 {
		g.P("types: [")
		g.In()

		sep := ","
		for i, mem := range union.Members {
			if i == memsLen-1 {
				sep = ""
			}
			g.P(mem.Name, sep)
		}

		g.Out()
		g.P("],")
	}

	g.Write(g.indent)
	g.WriteString("resolveType(value) { /* TODO */ }")

	if doc != nil && descr {
		g.printDescr(doc)

	}

	g.WriteByte('\n')
	g.Out()
	g.P("});")
}

func (g *Generator) generateEnum(imports *uint16, name string, descr bool, doc *ast.DocGroup, ts *ast.TypeSpec) {
	enum := ts.Type.(*ast.TypeSpec_Enum).Enum

	g.P("GraphQLEnumType({")
	g.In()

	g.P("name: '", name, "',")

	if doc != nil && descr {
		text := doc.Text()
		if len(text) > 0 {
			g.P("description: '", text[:len(text)-1], "',")
		}
	}

	g.P("values: {")
	g.In()

	valsLen := len(enum.Values.List)
	for i, v := range enum.Values.List {
		g.P(v.Name.Name, ": {")
		g.In()

		g.Write(g.indent)
		g.WriteString("value: '")
		g.WriteString(v.Name.Name)
		g.WriteByte('\'')

		if descr {
			g.printDescr(v.Doc)

		}

		g.WriteByte('\n')

		g.Out()

		g.Write(g.indent)
		g.WriteByte('}')
		if i != valsLen-1 {
			g.WriteByte(',')
		}
		g.WriteByte('\n')
	}

	g.Out()
	g.P("}")

	g.Out()
	g.P("});")
}

func (g *Generator) generateInput(imports *uint16, name string, descr bool, doc *ast.DocGroup, ts *ast.TypeSpec) {
	input := ts.Type.(*ast.TypeSpec_Input).Input

	g.P("GraphQLInputObjectType({")
	g.In()

	g.P("name: '", name, "',")

	g.P("fields: {")
	g.In()

	g.generateArgs(input.Fields.List, imports, descr)

	g.Out()
	g.Write(g.indent)
	g.WriteByte('}')

	if doc != nil && descr {
		g.printDescr(doc)

	}

	g.WriteByte('\n')

	g.Out()
	g.P("});")
}

func (g *Generator) generateDirective(imports *uint16, name string, descr bool, doc *ast.DocGroup, ts *ast.TypeSpec) {
	directive := ts.Type.(*ast.TypeSpec_Directive).Directive

	g.P("GraphQLDirectiveType({")
	g.In()

	g.P("name: '", name, "',")

	if doc != nil && descr {
		text := doc.Text()
		if len(text) > 0 {
			g.P("description: '", text[:len(text)-1], "',")
		}
	}

	// Print locations
	locsLen := len(directive.Locs)
	if locsLen == 1 {
		g.WriteString("locations: [ DirectiveLocation." + directive.Locs[0].Loc.String() + " ]")
	}
	if locsLen > 1 {
		g.P("locations: [")
		g.In()

		sep := ","
		for i, loc := range directive.Locs {
			if i == locsLen-1 {
				sep = ""
			}
			g.P("DirectiveLocation", ".", loc.Loc.String(), sep)
		}

		g.Out()
		g.Write(g.indent)
		g.WriteByte(']')
	}

	if directive.Args != nil {
		if locsLen > 0 {
			g.WriteByte(',')
			g.WriteByte('\n')
		}
		g.P("args: {")
		g.In()

		g.generateArgs(directive.Args.List, imports, descr)

		g.Out()
		g.Write(g.indent)
		g.WriteByte('}')
	}

	g.Out()
	g.P()
	g.P("});")
}

func (g *Generator) generateArgs(args []*ast.InputValue, imports *uint16, descr bool) {
	aLen := len(args) - 1
	for i, a := range args {
		g.P(a.Name.Name, ": {")
		g.In()

		g.Write(g.indent)
		g.WriteString("type: ")

		var fieldType interface{}
		switch v := a.Type.(type) {
		case *ast.InputValue_Ident:
			fieldType = v.Ident
		case *ast.InputValue_List:
			fieldType = v.List
		case *ast.InputValue_NonNull:
			fieldType = v.NonNull
		}
		g.printType(imports, fieldType)

		if a.Default != nil {
			g.WriteByte(',')
			g.WriteByte('\n')

			g.Write(g.indent)
			g.WriteString("defaultValue: ")

			var defType interface{}
			switch v := a.Default.(type) {
			case *ast.InputValue_BasicLit:
				defType = v.BasicLit
			case *ast.InputValue_CompositeLit:
				defType = v.CompositeLit
			}
			g.printVal(defType)
		}

		if descr {
			g.printDescr(a.Doc)
		}

		g.WriteByte('\n')

		g.Out()

		g.Write(g.indent)
		g.WriteByte('}')
		if i != aLen {
			g.WriteByte(',')
		}
		g.WriteByte('\n')
	}
}

func (g *Generator) printDescr(doc *ast.DocGroup) {
	text := doc.Text()
	if len(text) > 0 {
		g.WriteByte(',')
		g.WriteByte('\n')

		g.Write(g.indent)
		g.WriteString("description: '")

		g.WriteString(text[:len(text)-1])

		g.WriteByte('\'')
	}
}

// printType prints a field type
func (g *Generator) printType(imports *uint16, typ interface{}) {
	switch v := typ.(type) {
	case *ast.Ident:
		name := v.Name

		switch name {
		case "Int":
			name = "GraphQLInt"
			*imports &= ^intBit
		case "Float":
			name = "GraphQLFloat"
			*imports &= ^floatBit
		case "String":
			name = "GraphQLString"
			*imports &= ^stringBit
		case "Boolean":
			name = "GraphQLBoolean"
			*imports &= ^booleanBit
		case "ID":
			name = "GraphQLID"
			*imports &= ^idBit
		}

		g.WriteString(name)
	case *ast.List:
		g.WriteString("new GraphQLList(")

		switch w := v.Type.(type) {
		case *ast.List_Ident:
			typ = w.Ident
		case *ast.List_List:
			typ = w.List
		case *ast.List_NonNull:
			typ = w.NonNull
		}
		g.printType(imports, typ)

		g.WriteByte(')')

		*imports &= ^listBit
	case *ast.NonNull:
		g.WriteString("new GraphQLNonNull(")

		switch w := v.Type.(type) {
		case *ast.NonNull_Ident:
			typ = w.Ident
		case *ast.NonNull_List:
			typ = w.List
		}
		g.printType(imports, typ)

		g.WriteByte(')')

		*imports &= ^nonNullBit
	}
}

// printVal prints a value
func (g *Generator) printVal(val interface{}) {
	switch v := val.(type) {
	case *ast.BasicLit:
		s := v.Value
		if v.Kind == token.Token_STRING || v.Kind == token.Token_IDENT {
			s = strings.Trim(s, "\"")
			g.WriteByte('\'')
			g.WriteString(s)
			g.WriteByte('\'')
			return
		}
		g.WriteString(s)
	case *ast.ListLit:
		g.printList(v)
	case *ast.ObjLit:
		g.printObject(v)
	case *ast.CompositeLit:
		switch w := v.Value.(type) {
		case *ast.CompositeLit_BasicLit:
			g.printVal(w.BasicLit)
		case *ast.CompositeLit_ListLit:
			g.printVal(w.ListLit)
		case *ast.CompositeLit_ObjLit:
			g.printVal(w.ObjLit)
		}
	}
}

func (g *Generator) printList(v *ast.ListLit) {
	g.WriteByte('[')

	var vals []interface{}
	switch w := v.List.(type) {
	case *ast.ListLit_BasicList:
		for _, bval := range w.BasicList.Values {
			vals = append(vals, bval)
		}
	case *ast.ListLit_CompositeList:
		for _, cval := range w.CompositeList.Values {
			vals = append(vals, cval)
		}
	}

	vLen := len(vals) - 1
	for i, iv := range vals {
		g.printVal(iv)
		if i != vLen {
			g.WriteByte(',')
			g.WriteByte(' ')
		}
	}

	g.WriteByte(']')
}

func (g *Generator) printObject(v *ast.ObjLit) {
	g.WriteByte('{')
	g.WriteByte(' ')

	pLen := len(v.Fields) - 1
	for i, p := range v.Fields {
		g.WriteString(p.Key.Name)
		g.WriteString(": ")

		g.printVal(p.Val)

		if i != pLen {
			g.WriteByte(',')
		}
		g.WriteByte(' ')
	}

	g.WriteByte('}')
}

// P prints the arguments to the generated output.
func (g *Generator) P(str ...interface{}) {
	g.Write(g.indent)
	for _, s := range str {
		switch v := s.(type) {
		case []byte:
			g.Write(v)
		case string:
			g.WriteString(v)
		case bool:
			fmt.Fprint(g, v)
		case int:
			fmt.Fprint(g, v)
		case float64:
			fmt.Fprint(g, v)
		}
	}
	g.WriteByte('\n')
}

// In increases the indent.
func (g *Generator) In() {
	g.indent = append(g.indent, ' ', ' ')
}

// Out decreases the indent.
func (g *Generator) Out() {
	if len(g.indent) > 0 {
		g.indent = g.indent[:len(g.indent)-2]
	}
}

// getOptions returns a generator options struct given all generator option metadata from the Doc and CLI.
// Precedence: CLI over Doc over Default
//
func getOptions(doc *ast.Document, opts map[string]interface{}) (gOpts *Options, err error) {
	gOpts = &Options{
		Module:  "COMMONJS",
		declStr: commonJSDecl,
		imports: make([][]byte, 0, 15),
	}

	// Extract document directive options
	for _, d := range doc.Directives {
		if d.Name != "js" {
			continue
		}

		if d.Args == nil {
			break
		}

		jsOpts := d.Args.Args[0].Value.(*ast.Arg_CompositeLit).CompositeLit.Value.(*ast.CompositeLit_ObjLit).ObjLit
		for _, arg := range jsOpts.Fields {
			switch arg.Key.Name {
			case "module":
				gOpts.Module = arg.Val.Value.(*ast.CompositeLit_BasicLit).BasicLit.Value
			case "useFlow":
				v := arg.Val.Value.(*ast.CompositeLit_BasicLit).BasicLit.Value
				if v == "true" {
					gOpts.UseFlow = true
				}
			case "descriptions":
				b, err := strconv.ParseBool(arg.Val.Value.(*ast.CompositeLit_BasicLit).BasicLit.Value)
				if err != nil {
					return gOpts, err
				}

				gOpts.Descriptions = b
			}
		}
	}

	// Unmarshal cli options
	if opts == nil {
		return
	}
	if m, ok := opts["module"]; ok {
		gOpts.Module, _ = m.(string)
	}
	if d, ok := opts["descriptions"]; ok {
		gOpts.Descriptions, _ = d.(bool)
	}
	if u, ok := opts["useFlow"]; ok {
		gOpts.UseFlow, _ = u.(bool)
	}

	if gOpts.Module == "ES6" {
		gOpts.declStr = es6Decl
	}

	return
}
