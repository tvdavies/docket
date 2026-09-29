# hello-hooks

A minimal headless Docket plugin. It contributes:

- a Lua hook (`hooks/greet.lua`) that comments on tasks moved into `in-review`,
  delivered by the event runner (`delivery: service`);
- a `greeted` status after `in-review`;
- a workspace-scoped `greeting` setting;
- a CLI reachable as `docket hello-hooks`.

```sh
docket plugin add ./examples/plugins/hello-hooks
docket plugin enable hello-hooks
docket plugin config set hello-hooks --workspace . greeting='"Hi"'
docket hello-hooks ping
docket run --once      # deliver pending hook events and exit
```
