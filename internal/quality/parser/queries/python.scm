; Function and method definitions (decorated ones too: the decorator wraps
; the same function_definition node).
(function_definition
  name: (identifier) @function.name) @function.def

; Class definitions.
(class_definition
  name: (identifier) @type.name) @type.def

; Imports — `import a.b` and `import a.b as c`.
(import_statement
  name: (dotted_name) @import.path) @import.site

(import_statement
  name: (aliased_import
    name: (dotted_name) @import.path)) @import.site

; `from a.b import x` and `from ..a import x` — the whole statement, since
; each imported name can be a submodule.
(import_from_statement) @import.path @import.site

; Direct calls — `f()`.
(call
  function: (identifier) @call.name) @call.site

; Attribute calls — `obj.method()` or `mod.func()`.
(call
  function: (attribute
    attribute: (identifier) @call.name)) @call.site
