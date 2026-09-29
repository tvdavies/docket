import type {
  WidgetContext,
  WidgetInstance,
  WidgetModule,
  WidgetRenderState,
  WidgetSnapshot,
} from "./contracts";
import { installKit } from "./kit";
import { PluginUIError } from "./errors";
export interface WidgetElementHooks {
  mount(root: ShadowRoot, context: WidgetContext): WidgetInstance;
}
export interface WidgetElement extends HTMLElement {
  initialize(context: WidgetContext): void;
  update(
    snapshot: WidgetSnapshot,
    context: WidgetContext,
    view: WidgetRenderState,
  ): void;
  destroy(): void;
}
export function widgetElementName(widgetType: string) {
  if (!/^[a-z0-9][a-z0-9_-]*\/.+$/.test(widgetType))
    throw new PluginUIError("invalid_identity", "registration");
  return `docket-widget-${[...new TextEncoder().encode(widgetType)].map((b) => b.toString(16).padStart(2, "0")).join("")}-v2`;
}
/** Definitions cannot be removed. Code-changing reloads require a page reload. */
export function defineWidgetElement(
  widgetType: string,
  buildIdentity: string,
  hooks: WidgetElementHooks,
): string {
  const name = widgetElementName(widgetType);
  const old = customElements.get(name) as
    | (CustomElementConstructor & {
        widgetBuild?: string;
        widgetHooks?: WidgetElementHooks;
      })
    | undefined;
  if (old) {
    if (old.widgetBuild === buildIdentity && old.widgetHooks === hooks)
      return name;
    throw new PluginUIError("duplicate_definition", "registration");
  }
  class Element extends HTMLElement implements WidgetElement {
    static widgetBuild = buildIdentity;
    static widgetHooks = hooks;
    private instance?: WidgetInstance;
    private stopped = false;
    private initialized = false;
    initialize(context: WidgetContext) {
      if (this.initialized)
        throw new PluginUIError("duplicate_definition", "mount");
      this.initialized = true;
      const root = this.attachShadow({ mode: "open" });
      installKit(root);
      this.instance = hooks.mount(root, context);
    }
    update(
      snapshot: WidgetSnapshot,
      context: WidgetContext,
      view: WidgetRenderState,
    ) {
      if (!this.stopped) this.instance?.update(snapshot, context, view);
    }
    destroy() {
      if (this.stopped) return;
      this.stopped = true;
      const instance = this.instance;
      this.instance = undefined;
      try {
        instance?.destroy();
      } finally {
        this.shadowRoot?.replaceChildren();
      }
    }
    disconnectedCallback() {
      try {
        this.destroy();
      } catch {
        /* host already removes the body; disconnect must not leak errors */
      }
    }
  }
  customElements.define(name, Element);
  return name;
}
export function customElementWidget(
  options: Omit<WidgetModule, "mount"> & {
    buildIdentity: string;
    hooks: WidgetElementHooks;
  },
): WidgetModule {
  return {
    type: options.type,
    dataVersions: options.dataVersions,
    present: options.present,
    detail: options.detail,
    mount(body, context) {
      const name = defineWidgetElement(
        options.type,
        options.buildIdentity,
        options.hooks,
      );
      const element = document.createElement(name) as WidgetElement;
      try {
        element.initialize(context);
        body.append(element);
      } catch (error) {
        try {
          element.destroy();
        } finally {
          element.remove();
        }
        throw error;
      }
      return {
        update: (snapshot, ctx, view) => element.update(snapshot, ctx, view),
        destroy: () => {
          try {
            element.destroy();
          } finally {
            element.remove();
          }
        },
      };
    },
  };
}
