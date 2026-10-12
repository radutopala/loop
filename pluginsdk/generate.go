package pluginsdk

// The openapi subpackages hold the specs' types, which oapi-codegen
// generates.
//go:generate -command oapi-codegen go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0
//go:generate oapi-codegen -config openapi/pluginapi/oapi-codegen.yaml openapi/plugin-api-v1.yaml
//go:generate oapi-codegen -config openapi/plugincontract/oapi-codegen.yaml openapi/plugin-contract-v1.yaml
