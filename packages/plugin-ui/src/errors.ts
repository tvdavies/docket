import type { WidgetError, WidgetErrorCode } from './contracts';
/** Public messages are codes only; never include opaque data or service bodies. */
export class PluginUIError extends Error implements WidgetError {
  constructor(readonly code:WidgetErrorCode,readonly phase:WidgetError['phase'],readonly retryable=false){super(code);this.name='PluginUIError';}
}
