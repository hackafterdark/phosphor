package parser

// Templates provides human-readable query names mapped to tree-sitter S-expression patterns.
// The outer key is the language name (e.g., "go", "typescript"), the inner key is the template name.
var Templates = map[string]map[string]string{
	"go": {
		"find_functions": `
(function_declaration
  name: (identifier) @name
  parameters: (parameter_list) @parameters
  body: (block) @body)

(function_declaration
  name: (identifier) @name
  parameters: (parameter_list) @parameters)

(method_declaration
  receiver: (parameter_list) @receiver
  name: (field_identifier) @name
  parameters: (parameter_list) @parameters
  body: (block) @body)
`,

		"find_structs": `
(type_spec
  name: (type_identifier) @name
  type: (struct_type
    (field_declaration_list)))
`,

		"find_variables": `
(var_declaration
  (var_spec
    name: (identifier) @name
    value: (_) @value))

(var_declaration
  (var_spec
    name: (identifier)))

(short_var_declaration
  (expression_list
    (identifier) @name)
  (expression_list))

(short_var_declaration
  (expression_list
    (identifier) @name)
  (expression_list
    (_) @value))
`,

		"find_interfaces": `
(type_spec
  name: (type_identifier) @name
  type: (interface_type
    (method_elem
      (field_identifier) @method_name)))

(type_spec
  name: (type_identifier) @name
  type: (interface_type) @interface_body)
`,

		"find_calls": `
(call_expression
  function: (identifier) @function_name
  arguments: (argument_list) @arguments)

(call_expression
  function: (selector_expression
    field: (field_identifier) @method_name)
  arguments: (argument_list) @arguments)
`,

		"find_imports": `
(import_spec
  name: (package_identifier)? @package_name
  path: (interpreted_string_literal) @import_path)
`,

		"find_comments": `
(comment) @comment
`,
	},

	"typescript": {
		"find_functions": `
(function_declaration
  name: (identifier) @name
  parameters: (formal_parameters) @parameters
  body: (statement_block) @body)

(function_declaration
  name: (identifier) @name
  parameters: (_) @parameters)

(arrow_function
  parameters: (formal_parameters) @parameters
  body: (statement_block) @body)

(function_expression
  name: (identifier) @name
  parameters: (formal_parameters) @parameters
  body: (statement_block) @body)
`,

		"find_structs": `
(class_declaration
  name: (type_identifier) @name
  body: (class_body
    (method_definition
      name: (property_identifier) @method_name
      parameters: (formal_parameters) @parameters
      body: (statement_block) @body)))

(class_declaration
  name: (type_identifier) @name
  body: (class_body
    (_
      name: (property_identifier) @field_name)))

(type_alias_declaration
  name: (type_identifier) @name
  value: (_) @type_body)
`,

		"find_variables": `
(variable_declarator
  name: (identifier) @name
  value: (_) @value)

(variable_declarator
  name: (identifier) @name)

(variable_declarator
  name: (array_pattern) @name
  value: (_) @value)

(variable_declarator
  name: (object_pattern) @name
  value: (_) @value)
`,

		"find_interfaces": `
(interface_declaration
  name: (type_identifier) @name
  body: (interface_body))
`,

		"find_calls": `
(call_expression
  function: (identifier) @function_name
  arguments: (arguments) @arguments)

(call_expression
  function: (member_expression
    property: (property_identifier) @method_name)
  arguments: (arguments) @arguments)
`,

		"find_imports": `
(import_statement
  (import_clause
    (named_imports
      (import_specifier
        name: (identifier) @import_name)))
  (string) @import_path)

(import_statement
  (import_clause
    (namespace_import
      (identifier) @import_name))
  (string) @import_path)
`,

		"find_comments": `
(comment) @comment
`,
	},

	"javascript": {
		"find_functions": `
(function_declaration
  name: (identifier) @name
  parameters: (formal_parameters) @parameters
  body: (statement_block) @body)

(arrow_function
  parameters: (formal_parameters) @parameters
  body: (statement_block) @body)

(function_expression
  name: (identifier) @name
  parameters: (formal_parameters) @parameters
  body: (statement_block) @body)
`,

		"find_structs": `
(class_declaration
  name: (identifier) @name
  body: (class_body
    (method_definition
      name: (property_identifier) @method_name
      parameters: (formal_parameters) @parameters
      body: (statement_block) @body)))

(class_declaration
  name: (identifier) @name
  body: (class_body
    (field_definition
      property: (property_identifier) @field_name
      value: (_) @field_value)))

(class
  name: (identifier) @name
  body: (class_body
    (method_definition
      name: (property_identifier) @method_name
      parameters: (formal_parameters) @parameters
      body: (statement_block) @body)))
`,

		"find_variables": `
(variable_declarator
  name: (identifier) @name
  value: (_) @value)

(variable_declarator
  name: (identifier) @name)

(variable_declarator
  name: (array_pattern) @name
  value: (_) @value)

(variable_declarator
  name: (object_pattern) @name
  value: (_) @value)
`,

		"find_interfaces": `
`,

		"find_calls": `
(call_expression
  function: (identifier) @function_name
  arguments: (arguments) @arguments)

(call_expression
  function: (member_expression
    property: (property_identifier) @method_name)
  arguments: (arguments) @arguments)
`,

		"find_imports": `
(import_statement
  (import_clause
    (named_imports
      (import_specifier
        name: (identifier) @import_name)))
  (string) @import_path)

(import_statement
  (import_clause
    (namespace_import
      (identifier) @import_name))
  (string) @import_path)
`,

		"find_comments": `
(comment) @comment
`,
	},

	"python": {
		"find_functions": `
(function_definition
  name: (identifier) @name
  parameters: (parameters) @parameters
  body: (block) @body)
`,

		"find_structs": `
(class_definition
  name: (identifier) @name
  body: (block) @body)
`,

		"find_variables": `
;; Local variables
(assignment left: (identifier) @name)

;; Class attributes (self.x = y)
(expression_statement
  (assignment
    left: (attribute object: (identifier) @instance attribute: (identifier) @name)))
`,

		"find_calls": `
(call
  function: [
    (identifier) @function_name
    (attribute 
      object: (_) 
      attribute: (identifier) @function_name)
  ])
`,

		"find_imports": `
(import_statement (dotted_name) @name)
(import_from_statement module_name: (dotted_name) @module)
`,

		"find_comments": `
(comment) @comment

;; Capture docstrings as comments
(module . (expression_statement (string) @docstring))
(function_definition . (block (expression_statement (string) @docstring)))
(class_definition . (block (expression_statement (string) @docstring)))
`,
	},

	"sql": {
		"find_functions": `
(create_function
  (object_reference
    (identifier) @name)
  (function_body) @body)
`,
		"find_calls": `
(invocation
  (object_reference
    (identifier) @function_name))
`,

		"find_structs": `
(create_table
  (object_reference
    (identifier) @name)
  (column_definitions) @body)
`,

		"find_select_tables": `
(statement
  (select)
  (from
    (relation
      (object_reference
        (identifier) @table_name))))

(statement
  (select)
  (from
    (object_reference
      (identifier) @table_name)))
`,

		"find_joins": `
(join
  (relation
    (object_reference
      (identifier) @joined_table)))
`,

		"find_inserts": `
(insert
  (object_reference
    (identifier) @table_name))
`,

		"find_deletes": `
(statement
  (delete)
  (from
    (object_reference
      (identifier) @table_name)))
`,

		"find_select_all": `
(select
  (select_expression
    (term
      (all_fields) @all)))
`,
		"find_comments": `
(comment) @comment
`,
	},

	"rust": {
		"find_functions": `
(function_item
  name: (identifier) @name
  parameters: (parameters)? @parameters
  body: (block)? @body)
`,

		"find_structs": `
(struct_item
  name: (type_identifier) @name
  body: (field_declaration_list)? @body)
`,

		"find_variables": `
(let_declaration
  pattern: (identifier) @name
  value: (_)? @value)
`,

		"find_interfaces": `
(trait_item
  name: (type_identifier) @name)
`,

		"find_calls": `
(call_expression
  function: (_) @function_name)
`,

		"find_imports": `
(use_declaration
  argument: (_) @import_path)
`,

		"find_comments": `
(line_comment) @comment

(block_comment) @comment
`,
	},

	// "java" — Java not supported (requires external scanner not present in vendored grammar)
	"php": {
		"find_functions": `
(function_definition
  name: (name) @name
  body: (compound_statement) @body)

(method_declaration
  name: (name) @name
  body: (compound_statement) @body)

(anonymous_function
  parameters: (formal_parameters) @parameters
  body: (compound_statement) @body)
`,

		"find_structs": `
(class_declaration
  name: (_) @name
  body: (declaration_list) @class_body)
`,

		"find_variables": `
(variable_name) @name

(property_declaration
  (property_element
    name: (variable_name) @name
    default_value: (_) @value))

(property_declaration
  (property_element
    name: (variable_name) @name))

(assignment_expression
  left: (variable_name) @name
  right: (_) @value)
`,

		"find_interfaces": `
(interface_declaration
  name: (name) @name
  body: (declaration_list) @interface_body)
`,

		"find_calls": `
(function_call_expression
  function: (_) @function_name
  arguments: (arguments) @arguments)

(member_call_expression
  object: (_)
  (name) @method_name
  arguments: (arguments) @arguments)

(scoped_call_expression
  scope: (_)
  (name) @method_name
  arguments: (arguments) @arguments)
`,

		"find_imports": `
(namespace_use_clause
  .
  [
    (qualified_name)
    (name)
  ] @import_path
  (name)? @import_alias)
`,

		"find_comments": `
(comment) @comment
`,
	},

	"cpp": {
		"find_functions": `
(function_definition
  (function_declarator
    (identifier) @name
    (parameter_list) @parameters)
  (compound_statement) @body)

(function_definition
  (function_declarator
    (field_identifier) @name
    (parameter_list) @parameters)
  (compound_statement) @body)
`,

		"find_structs": `
(class_specifier
  name: (type_identifier) @name
  body: (field_declaration_list) @body)

(struct_specifier
  name: (type_identifier) @name
  body: (field_declaration_list) @body)
`,

		"find_variables": `
(declaration
  (init_declarator
    (identifier) @name
    value: (_) @value))

(assignment_expression
  left: (identifier) @name
  right: (_) @value)
`,

		"find_interfaces": ``,

		"find_calls": `
(call_expression
  function: (identifier) @function_name
  arguments: (argument_list) @arguments)

(call_expression
  function: (qualified_identifier
    (identifier) @function_name)
  arguments: (argument_list) @arguments)

(call_expression
  function: (field_expression
    field: (field_identifier) @method_name)
  arguments: (argument_list) @arguments)
`,

		"find_imports": `
(preproc_include
  path: (system_lib_string) @import_path)

(preproc_include
  path: (string_literal) @import_path)
`,

		"find_comments": `
(comment) @comment
`,
	},

	"hcl": {
		"find_functions": `
(attribute
  (_) @name
  (_) @body)
`,

		"find_structs": `
(block
  (_) @name
  (_) @body)
`,

		"find_variables": `
(attribute
  (_) @name
  (_) @value)
`,

		"find_interfaces": ``,

		"find_calls": `
(function_call
  (_) @function_name
  (_) @arguments)
`,

		"find_imports": ``,

		"find_comments": `
(comment) @comment
`,
	},

	"csharp": {
		"find_functions": `
(method_declaration
  name: (identifier) @name
  parameters: (parameter_list) @parameters
  body: (block) @body)

(method_declaration
  name: (identifier) @name
  parameters: (parameter_list) @parameters
  body: (arrow_expression_clause) @body)

(constructor_declaration
  name: (identifier) @name
  parameters: (parameter_list) @parameters
  body: (block) @body)
`,

		"find_structs": `
(class_declaration
  name: (identifier) @name
  body: (declaration_list) @body)

(record_declaration
  name: (identifier) @name
  body: (declaration_list) @body)
`,

		"find_variables": `
(local_declaration_statement
  (variable_declaration
    (variable_declarator
      (identifier) @name)))
`,

		"find_interfaces": ``,

		"find_calls": `
(invocation_expression
  function: (identifier) @function_name
  arguments: (argument_list) @arguments)

(invocation_expression
  function: (member_access_expression
    (identifier) @method_name)
  arguments: (argument_list) @arguments)
`,

		"find_imports": `
(using_directive
  (identifier) @import_path)

(using_directive
  (qualified_name
    (identifier) @import_path))
`,

		"find_comments": `
(comment) @comment
`,
	},

	"ruby": {
		"find_functions": `
(method
  name: (_) @name)

(singleton_method
  name: (_) @name)
`,

		"find_structs": `
(class
  name: (_) @name)

(class
  (constant) @name)
`,

		"find_variables": `
(assignment
  left: (_) @name)
`,

		"find_interfaces": ``,

		"find_calls": `
(call
  method: (_) @method_name)
`,

		"find_imports": ``,

		"find_comments": `
(comment) @comment
`,
	},

	"json": {
		"find_functions": ``,

		"find_structs": `
(object
  (pair
    key: (string) @name
    value: (object) @body))
`,

		"find_variables": `
(pair
  key: (string) @name
  value: (_) @value)
`,

		"find_interfaces": ``,

		"find_calls": ``,

		"find_imports": ``,

		"find_comments": `
(comment) @comment
`,
	},

	"html": {
		"find_functions": ``,

		"find_structs": `
(element
  (start_tag
    (tag_name) @name))

(self_closing_tag
  (tag_name) @name)
`,

		"find_variables": `
(attribute
  (attribute_name) @name
  (_) @value)
`,

		"find_interfaces": ``,

		"find_calls": ``,

		"find_imports": `
(element
  (start_tag
    (tag_name) @tag_name
    (attribute
      (attribute_name) @attr_name
      (_) @import_path)))

(self_closing_tag
  (tag_name) @tag_name
  (attribute
    (attribute_name) @attr_name
    (_) @import_path))
`,

		"find_comments": `
(comment) @comment
`,
	},

	"css": {
		"find_functions": ``,

		"find_structs": `
(rule_set
  (selectors) @name
  (block) @body)

(media_statement
  (feature_query) @name
  (block) @body)
`,

		"find_variables": `
(declaration
  (property_name) @name
  (call_expression) @value)

(declaration
  (property_name) @name
  (color_value) @value)

(declaration
  (property_name) @name
  (integer_value) @value)

(declaration
  (property_name) @name
  (float_value) @value)

(declaration
  (property_name) @name
  (string_value) @value)

(declaration
  (property_name) @name
  (plain_value) @value)
`,

		"find_interfaces": ``,

		"find_calls": `
(call_expression
  (function_name) @name
  (arguments) @arguments)
`,

		"find_imports": `
(import_statement
  (string_value) @import_path)
`,

		"find_comments": `
(comment) @comment
`,
	},

	"toml": {
		"find_functions": ``,

		"find_structs": `
(table
  (dotted_key) @name)

(table_array_element
  (dotted_key) @name)
`,

		"find_variables": `
(pair
  (dotted_key) @name
  (_) @value)

(pair
  (dotted_key) @name)
`,

		"find_interfaces": ``,

		"find_calls": ``,

		"find_imports": ``,

		"find_comments": `
(comment) @comment
`,
	},

	"scala": {
		"find_functions": `
(function_definition
  name: (_) @name)

(function_definition
  name: (_) @name
  parameters: (_) @parameters)

(function_definition
  name: (_) @name
  parameters: (_) @parameters
  body: (_) @body)

(function_declaration
  name: (_) @name)
`,

		"find_structs": `
(class_definition
  name: (_) @name
  body: (_) @body)

(object_definition
  name: (_) @name
  body: (_) @body)
`,

		"find_variables": `
(val_definition
  pattern: (_) @name
  value: (_) @value)

(val_definition
  pattern: (_) @name)

(var_definition
  pattern: (_) @name
  value: (_) @value)

(var_definition
  pattern: (_) @name)
`,

		"find_interfaces": `
(trait_definition
  name: (_) @name
  body: (_) @body)
`,

		"find_calls": `
(call_expression
  function: (_) @function_name
  arguments: (_))
`,

		"find_imports": `
(import_declaration
  path: (_) @import_path)
`,

		"find_comments": `
(comment) @comment
`,
	},

	"c": {
		"find_functions": `
(function_definition
  declarator: (function_declarator
    parameters: (parameter_list) @parameters)
  body: (compound_statement) @body)
`,

		"find_structs": `
(struct_specifier
  name: (type_identifier) @name
  body: (field_declaration_list) @body)

(type_definition
  (struct_specifier
    body: (field_declaration_list) @body)
  (type_identifier) @name)
`,

		"find_variables": `
(init_declarator
  declarator: (identifier) @name
  value: (_) @value)
`,

		"find_interfaces": ``,

		"find_calls": `
(call_expression
  function: (identifier) @function_name
  arguments: (argument_list) @arguments)

(call_expression
  function: (field_expression
    field: (field_identifier) @method_name)
  arguments: (argument_list) @arguments)
`,

		"find_imports": `
(preproc_include
  path: (string_literal) @import_path)

(preproc_include
  path: (system_lib_string) @import_path)
`,

		"find_comments": `
(comment) @comment
`,
	},

	"bash": {
		"find_functions": `
(function_definition
  name: (word) @name
  body: (compound_statement) @body)

(function_definition
  name: (word) @name)
`,

		"find_structs": ``,

		"find_variables": `
(variable_assignment
  name: (variable_name) @name
  value: (_) @value)

(variable_assignment
  name: (variable_name) @name)
`,

		"find_interfaces": ``,

		"find_calls": `
(command
  name: (command_name (word)) @function_name)

(command
  name: (command_name (word)) @function_name
  argument: (_) @arguments)
`,

		"find_imports": ``,

		"find_comments": `
(comment) @comment
`,
	},
}

// GetTemplate returns the tree-sitter query for the given language and template name.
// Returns an empty string and false if the template doesn't exist.
func GetTemplate(lang, name string) (string, bool) {
	return Registry.GetTemplate(lang, name)
}

// TemplateNames returns a sorted list of available template names for the given language.
func TemplateNames(lang string) []string {
	return Registry.TemplateNames(lang)
}
