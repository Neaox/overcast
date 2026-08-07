+ [ui] add copy URL buttons for S3 buckets, objects, and prefixes (S3 URI and path-style formats, actions-menu UX)
+ [ui] add copy endpoint button to header alongside connection status indicator
+ [ui] overhaul connection dialog with debounced endpoint validation (spinner/tick/cross + tooltip), combobox suggestions, and dynamic label placeholder
+ [ui] auto-detect API port when known (Docker socket, native, or 1:1 mapping); show connection dialog when unknown with Docker socket guidance
+ [ui] extract endpoint status indicator (dot + baseUrl + copy) into standalone `HeaderEndpoint` component
+ [ui] connection settings (Plug icon) always visible — opens modal when connected, resets dialog when unconfigured
+ [bff] `normalizeEndpoint` now rewrites all loopback endpoints to internal API port, fixing BFF proxying in Docker with remapped ports and no socket
+ [bff] `deriveAPIBaseURL` returns endpoint known/unknown alongside URL; handles native custom API port via default-UI-port fallback
+ [combobox] add `allowFreeText` prop for editable-after-selection behaviour (seeds query from current value, commits on blur)