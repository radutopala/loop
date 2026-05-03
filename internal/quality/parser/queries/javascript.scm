; Function and method definitions.
(function_declaration
  name: (identifier) @function.name) @function.def

(method_definition
  name: (property_identifier) @function.name) @function.def

; Class definitions (JavaScript has no interface or type-alias forms).
(class_declaration
  name: (identifier) @type.name) @type.def

; Imports — `import x from 'mod'` and `import 'mod'`.
(import_statement
  source: (string (string_fragment) @import.path)) @import.site

; Re-exports — `export ... from 'mod'`.
(export_statement
  source: (string (string_fragment) @import.path)) @import.site

; Direct calls — `f()`.
(call_expression
  function: (identifier) @call.name) @call.site

; Member calls — `obj.method()`.
(call_expression
  function: (member_expression
    property: (property_identifier) @call.name)) @call.site
