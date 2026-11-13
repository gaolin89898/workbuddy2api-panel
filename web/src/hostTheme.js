// The DSH iframe shares its origin with the host. Read only its public CSS
// tokens; standalone pages keep their own theme and saved preference.
export function followHostTheme(onChange) {
  if (window.parent === window) return () => {};
  let host;
  try { host = window.parent.document; } catch { return () => {}; }

  const tokens = {
    page: '--dsw-alias-bg-base',
    surface: '--dsw-alias-bg-layer-1',
    'surface-secondary': '--dsw-alias-bg-layer-2',
    line: '--dsw-alias-border-l2',
    ink: '--dsw-alias-label-primary',
    secondary: '--dsw-alias-label-secondary',
    muted: '--dsw-alias-label-tertiary',
    // brand-text is a foreground token and is black in the light DSH theme;
    // use the host's explicit blue action token for charts and accents.
    accent: '--dsw-static-blue-600',
    'switch-on': '--dsw-static-blue-600',
    'switch-off': '--dsw-static-neutral-550',
    'accent-soft': '--dsw-alias-interactive-bg-active',
    warning: '--dsw-alias-state-warn-label',
    'warning-soft': '--dsw-alias-state-warn-tertiary',
    danger: '--dsw-alias-state-error-primary',
    'danger-soft': '--dsw-alias-interactive-bg-hover-danger',
    button: '--dsw-alias-button-primary-fill',
    'button-hover': '--dsw-alias-button-primary-hover',
    'button-text': '--dsw-alias-label-primary-foreground',
    sidebar: '--dsw-specific-sidebar-fill',
    'nav-active': '--dsw-specific-sidebar-nav-item-active',
    hover: '--dsw-alias-interactive-bg-hover',
    scrollbar: '--dsh-scrollbar-thumb',
    'scrollbar-hover': '--dsh-scrollbar-thumb-hover',
  };
  let frame;
  const sync = () => {
    frame = undefined;
    const style = window.parent.getComputedStyle(host.body);
    if (!style.getPropertyValue(tokens.page).trim()) return;
    const root = document.documentElement;
    for (const [name, token] of Object.entries(tokens)) {
      const value = style.getPropertyValue(token).trim();
      if (value && CSS.supports('color', value)) root.style.setProperty(`--host-${name}`, value);
    }
    const width = style.getPropertyValue('--dsh-scrollbar-width').trim();
    if (width && CSS.supports('width', width)) root.style.setProperty('--host-scrollbar-width', width);
    // 主按钮底色与文字色同色时（个别 DSH 皮肤变量缺失导致），按宿主深浅
    // 强制写入对比色，避免白底白字按钮。
    const fill = style.getPropertyValue(tokens.button).trim();
    const fg = style.getPropertyValue(tokens['button-text']).trim();
    if (fill && fg && CSS.supports('color', fill) && CSS.supports('color', fg) && fill === fg) {
      root.style.setProperty('--host-button-text', host.body.hasAttribute('data-ds-dark-theme') ? '#10131a' : '#ffffff');
    }
    root.dataset.hostTheme = document.body.dataset.hostTheme = 'dsh';
    onChange(host.body.hasAttribute('data-ds-dark-theme'));
  };
  const schedule = () => { frame ??= requestAnimationFrame(sync); };
  const observer = new MutationObserver(schedule);
  observer.observe(host.documentElement, { attributes: true });
  observer.observe(host.body, { attributes: true });
  observer.observe(host.head, { attributes: true, childList: true, characterData: true, subtree: true });
  sync();
  return () => { observer.disconnect(); if (frame !== undefined) cancelAnimationFrame(frame); };
}
