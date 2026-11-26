export const drawn: { options: Record<string, unknown> }[] = [];
export const zrEvents: string[] = [];

const noop = () => {};
type Aria = { aria?: { label?: { description?: string } } };

const instance = (dom: HTMLElement) => {
  const entry = { options: {} as Record<string, unknown> };
  drawn.push(entry);
  return new Proxy({
    setOption: (o: Record<string, unknown> & Aria) => {
      entry.options = { ...entry.options, ...o };
      const said = o.aria?.label?.description;
      if (said) dom.setAttribute("aria-label", said);
    },
    getZr: () => ({ on: noop, off: noop, handler: { trigger: (name: string) => zrEvents.push(name) } }),
    isDisposed: () => false,
  } as Record<string, unknown>, { get: (t, k) => (k in t ? t[k as string] : noop) });
};

export const echarts = {
  init: instance,
  use: noop,
  graphic: { LinearGradient: class { constructor(...args: unknown[]) { Object.assign(this, { args }); } } },
};
