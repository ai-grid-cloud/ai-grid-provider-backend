# ConfigMaps the fake hub serves

`make dev-fake` starts the fake hub with `--configmaps fixtures/configmaps`. A `GET` of ConfigMap
`NAME` in namespace `NAMESPACE` reads `NAMESPACE/NAME.yaml` here on every request, so an edited or
added file applies at once.

- `ai-grid/ai-grid-serving-options.yaml`: the serving options defaults
  (`AI_GRID_SERVING_OPTIONS_CONFIGMAP`), as the console's Helm chart renders them.
- `ai-grid/ai-grid-serving-options-custom.yaml`: not shipped. Add it to try the provider admin's
  custom list (`AI_GRID_SERVING_OPTIONS_CUSTOM_CONFIGMAP`), for example:

  ```yaml
  apiVersion: v1
  kind: ConfigMap
  metadata:
    name: ai-grid-serving-options-custom
    namespace: ai-grid
  data:
    serving-options.yaml: |
      baseRefs:
      - name: v3-6-0-kserve-config-llm-template-cpu
        displayName: vLLM CPU (ours)
      - name: v3-6-0-kserve-config-llm-template-intel-gaudi
        displayName: vLLM Intel Gaudi
        accelerators: [habana.ai/gaudi]
  ```

  Delete it to go back to the defaults alone.
