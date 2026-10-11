// Package pluginsdk is for writing Loop plugins in Go.
//
// A plugin is a container Loop starts per channel. Loop calls into it (the
// Loop → plugin half of the contract, which Server serves) and it calls Loop
// back through the Plugin API v1 (Client). Env reads what Loop passes the
// container, and Manifest is its plugin.json. Package pluginsdktest holds a
// fake Loop and a conformance check for a plugin's tests.
//
// A plugin's main is typically:
//
//	env := pluginsdk.LoadEnv(os.Getenv)
//	secret, err := env.Secret()
//	// handle err
//	m, err := pluginsdk.ReadManifest(pluginsdk.ManifestPath)
//	// handle err
//	api := env.Client()
//	srv := &pluginsdk.Server{
//		Secret:  secret,
//		Version: m.Version,
//		Actions: map[string]pluginsdk.ActionFunc{"daily": daily(api)},
//		X:       uiAPI,
//	}
//	defer srv.Close()
//	ln, err := env.Listen()
//	// handle err
//	return pluginsdk.Serve(ctx, ln, srv.Handler())
package pluginsdk
