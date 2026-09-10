# Plugin settings implementation coverage (JOB-0094)

The implementation follows [approved plan v1](https://plans.myslop.app/p/8901f1ba7f).
The task worktree is project `docket`, branch
`tvd/render-schema-generated-plugin-settings-at-every`, based on `a019053`.
No storage, PATCH, plugin-card/resolver or Dispatch pipeline contract changes are included.

## Requirements to implementation and evidence

| Requirement | Code | Verification |
| --- | --- | --- |
| Discoverable instance, board and composed-lane settings | `App.tsx`, `router.ts`, `BoardView.tsx`, `SettingsPage.tsx`; exact instance SPA allowlist in `internal/service/http.go` | Bun route tests, Go SPA route/CSP test, production-browser deep links and lane selector; existing UI smoke suite |
| Every supported type, typed enums, required/default/unset distinctions | `FieldEditor.tsx`, `model.ts`, shared `docs/plugin-ui.d.ts` types | Fixture manifest has all five kinds at each scope; Go and real-browser persistence checks; Bun boolean/number/compound enum, finite number, JSON and required-presence cases |
| Correct owning storage and inheritance | `buildFields`, `storageDescription` | Go effective-config/default assertions; browser checks instance provenance, board default precedence and fallback, independent lane and two-workspace storage bytes |
| No secret inputs, values, defaults or payloads | Catalogue secret filtering, generated read-only environment instructions, payload secret exclusion | Go secret rejection/catalogue tests; Bun secret sanitization and payload tests; browser environment sentinel checks before screenshots and in requests/storage |
| Touched-key saves, atomic failures, no reset/delete affordance | `useSettingsForm`, scoped API wrappers; existing Go endpoints unchanged | Go byte-for-byte rejection cases (type, enum, unknown key, secret, required absence, origin/content-type), typed whole-field replacement; browser raw invalid JSON, real API rejections and actual required-field save rejection |
| Retained drafts, preflight conflict checks, explicit read-back | `useCatalogue`, `useSettingsForm`, `ScopedForm` | Bun duplicate-submit, raw JSON, schema/version, unavailable scope, unmounted preflight, confirmed-save/read failure, uncertain transport and semantically unchanged saves; browser manifest changes, malformed catalogue, lost save response and read-only retry |
| Navigation guards and response ownership | `useSettingsNavigation`, identity-keyed settings pages/forms | Browser cancelled Back/Forward, Classic, workspace and command-palette navigation; unload guard; delayed beta PATCH blocks leaving and cannot alter alpha; Bun unmounted preflight does not PATCH |
| Degraded operation, unknown versions/schemas, absent plugins/workspaces | `SettingsPage`, runtime schema checks | Browser zero workspaces, unavailable board, unknown lane, removed plugin with retained draft, empty catalogue, supported v99 schema; Bun malformed schema keeps existing drafts read-only |
| Keyboard, labels, errors, responsive light/dark layout | Native controls/shared primitives, fieldset/legend, described errors, error-summary links, post-render field focus; module-local `settings.css` | Browser heading focus, Tab editing, mobile width/height checks and screenshots; Bun server rejection focus and `aria-invalid`; no browser runtime errors |
| Safe repeatable preview and evidence | `settings-sandbox.ts`, `verify-settings.ts` | Fresh temporary registry, plugin copy and two workspaces; explicit Docket/config/XDG overrides; inherited task/session pointers cleared; no handlers; random loopback port; owned process cleanup. Preview shares the sandbox factory but runs no test mutations |

All paths under `web/src/views/settings/` unless otherwise noted. Tests live in
`web/tests/settings-{model,form}.test.*`, `internal/service/plugin_settings_test.go`
and `web/scripts/verify-settings.ts`.

## Commands

See [the preview/test recipe](../web/tests/README.md#plugin-settings-real-api-checks).
The Docket task and PR record the exact tested head, command output and screenshot
URLs; generated evidence is not embedded in the production web bundle.

## Implementation self-review

Reviewed the full base-to-head source change for data ownership, security,
concurrency, malformed schemas/responses, draft lifecycle, navigation, keyboard
focus and responsive layout. Verified findings fixed during implementation:

- A rapid navigation could run before a passive dirty notification. Dirty/pending
  state now reaches the navigation guard in a layout effect.
- A malformed successful PATCH response now reports an uncertain result rather
  than claiming success.
- Discard cannot bypass required read-back after an uncertain or confirmed-but-
  unreloaded save. Read-back retry never sends another PATCH.
- A formatting-only JSON save with an unchanged server fingerprint now becomes
  clean after confirmation. Compound-enum selects do not offer a JSON-format action.
- Board fingerprints include their instance fallback inputs, so an external
  instance edit refreshes effective-value explanations and blocks stale drafts.
- Unsupported schema refreshes keep prior drafts, block saving/rebasing, and do
  not attempt to construct controls from malformed fields.
- Server rejection focuses the invalid field after the pending fieldset is
  re-enabled; focus callbacks are cancelled on unmount.
- Indexed Back/Forward restoration preserves the cancelled route and history;
  task and settings navigation use the same indexed history writer.
- Mobile hidden-checkbox positioning no longer contributes document overflow;
  instance-settings discovery and the attribution footer stay available on
  small screens.

## Deliberate limits

- The API has no revision/CAS token. Preflight catches observable changes, but
  concurrent writes to the same field can still be last-writer-wins.
- Instance values are default-resolved; the UI cannot identify stored versus
  default provenance. Workspace defaults override same-key instance values.
- There is no delete/reset API. Empty strings, false, zero, lists and maps are
  values, not deletion. Discard changes only unsaved editor state.
- Configuration saves do not prove plugin service health. The fixture declares
  an unavailable optional service and valid configuration saves still succeed.
- Workspace runtime recovery can lag an operator's repair. Reload configuration
  retries availability checks; stale/unavailable forms remain non-writable.
- Browser checks use Chromium, including its native confirmation dialogs.
  Other browser engines and a manual screen-reader session remain review checks.
