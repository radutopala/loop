; Function and method definitions.
(function_declaration
  name: (identifier) @function.name) @function.def

(method_declaration
  name: (field_identifier) @function.name) @function.def

; Type definitions.
(type_spec
  name: (type_identifier) @type.name) @type.def

; Imports.
(import_spec
  path: (interpreted_string_literal) @import.path) @import.site

; Direct calls — `pkg()`.
(call_expression
  function: (identifier) @call.name) @call.site

; Method or qualified calls — `recv.method()` or `pkg.func()`.
(call_expression
  function: (selector_expression
    field: (field_identifier) @call.name)) @call.site
