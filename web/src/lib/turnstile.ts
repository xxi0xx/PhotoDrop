export type TurnstileAPI = {
  render(element: HTMLElement, options: Record<string, unknown>): string;
  remove(id: string): void;
  reset(id: string): void;
};
declare global { interface Window { turnstile?: TurnstileAPI } }
let loading: Promise<TurnstileAPI> | undefined;
// Called only from the configured challenge component, after photo selection.
export function loadTurnstile(): Promise<TurnstileAPI> {
  if (window.turnstile) return Promise.resolve(window.turnstile);
  if (!loading) loading = new Promise<TurnstileAPI>((resolve, reject) => {
    const script = document.createElement('script');
    const failed = () => { clearTimeout(timeout); script.remove(); loading = undefined; reject(new Error('Verification could not load. Check your connection and try again.')); };
    const timeout = setTimeout(failed, 15000);
    script.src = 'https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit';
    script.async = true; script.defer = true;
    script.onerror = failed;
    script.onload = () => { clearTimeout(timeout); if (window.turnstile) resolve(window.turnstile); else failed(); };
    document.head.append(script);
  });
  return loading;
}
