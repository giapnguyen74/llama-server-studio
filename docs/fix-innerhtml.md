Replace innerHTML with safe DOM APIs
The cheapest fix, no framework needed:

element.textContent = value — never parses HTML, just text.
element.setAttribute('href', value) plus a scheme allowlist (reject anything that isn't http:, https:, mailto:).
Build structure with document.createElement + appendChild rather than string concatenation.

A small helper covers 90% of cases:
jsfunction h(tag, attrs = {}, ...children) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (k.startsWith('on')) continue;           // no inline handlers
    if (k === 'href' || k === 'src') {
      if (!/^(https?:|mailto:|\/)/i.test(v)) continue;
    }
    el.setAttribute(k, v);
  }
  for (const c of children) {
    el.append(c instanceof Node ? c : document.createTextNode(c));
  }
  return el;
}
That's ~15 lines and removes the need for innerHTML entirely.