import { StrictMode, Component, type ErrorInfo, type ReactNode } from 'react';
import { createRoot } from 'react-dom/client';
import { App } from './app/App';
import { loadBuiltinPluginUI } from './registry/registry';
import './styles.css';

loadBuiltinPluginUI();

class AppBoundary extends Component<{ children: ReactNode }, { error: Error | null }> {
  state: { error: Error | null } = { error: null };
  static getDerivedStateFromError(error: Error) { return { error }; }
  componentDidCatch(error: Error, info: ErrorInfo) { console.error(error, info); }
  render() {
    if (this.state.error) return <main className="boot-screen"><span className="brand-mark">D</span><h1>Docket could not start</h1><p>{this.state.error.message}</p><a href="/classic">Open the classic board</a></main>;
    return this.props.children;
  }
}

createRoot(document.getElementById('root')!).render(<StrictMode><AppBoundary><App /></AppBoundary></StrictMode>);
