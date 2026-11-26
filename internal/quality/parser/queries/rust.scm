; Functions, methods, and trait method signatures.
(function_item
  name: (identifier) @function.name) @function.def

(function_signature_item
  name: (identifier) @function.name) @function.def

; Type definitions.
(struct_item
  name: (type_identifier) @type.name) @type.def

(enum_item
  name: (type_identifier) @type.name) @type.def

(union_item
  name: (type_identifier) @type.name) @type.def

(trait_item
  name: (type_identifier) @type.name) @type.def

(type_item
  name: (type_identifier) @type.name) @type.def

; Imports — the whole `use` tree; the parser expands braces into paths.
(use_declaration
  argument: (_) @import.path) @import.site

; `mod foo;` loads foo.rs or foo/mod.rs. Inline `mod foo { … }` is dropped
; by the parser since it names no file.
(mod_item
  name: (identifier) @import.module) @import.site

; Direct calls — `f()`.
(call_expression
  function: (identifier) @call.name) @call.site

; Method calls — `recv.method()`.
(call_expression
  function: (field_expression
    field: (field_identifier) @call.name)) @call.site

; Path calls — `Type::new()` or `module::func()`.
(call_expression
  function: (scoped_identifier
    name: (identifier) @call.name)) @call.site
