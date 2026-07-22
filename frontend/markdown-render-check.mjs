import fs from 'node:fs';
import ts from 'typescript';
import vm from 'node:vm';

const source = fs.readFileSync(new URL('./src/app/lib/utils.ts', import.meta.url), 'utf8');
const start = source.indexOf('export function escapeBasic');
const end = source.indexOf('export function readImageAttachment');

if (start === -1 || end === -1 || end <= start) {
  throw new Error('failed to locate Markdown renderer source');
}

const rendererSource = source
  .slice(start, end)
  .replace(/export function /g, 'function ');

const context = {};
const { outputText } = ts.transpileModule(
  `${rendererSource}\nglobalThis.renderMarkdown = renderMarkdown;`,
  {
    compilerOptions: {
      module: ts.ModuleKind.ESNext,
      target: ts.ScriptTarget.ES2020,
    },
  },
);
vm.runInNewContext(outputText, context);

const html = context.renderMarkdown(`# Title

| Path | Purpose |
| --- | --- |
| \`internal/web/static\` | Embedded UI |

- **bold**
- _emphasis_

> quoted

<script>alert(1)</script>

[docs](https://example.com)

\`\`\`ts
const value = "<safe>";
\`\`\`
`);

assertIncludes(html, '<table');
assertIncludes(html, '<thead>');
assertIncludes(html, '<tbody>');
assertIncludes(html, '<th');
assertIncludes(html, '<td');
assertIncludes(html, '<h1');
assertIncludes(html, '<ul');
assertIncludes(html, '<blockquote');
assertIncludes(html, '<strong>bold</strong>');
assertIncludes(html, '<em>emphasis</em>');
assertIncludes(html, 'href="https://example.com"');
assertIncludes(html, '&lt;script&gt;alert(1)&lt;/script&gt;');
assertIncludes(html, 'const value = &quot;&lt;safe&gt;&quot;');

if (html.includes('<script>')) {
  throw new Error('raw script tag was rendered');
}

const compactFenceHTML = context.renderMarkdown('```textHWQ_U/,\n```');
assertIncludes(compactFenceHTML, '<pre');
assertIncludes(compactFenceHTML, 'HWQ_U/,');
if (compactFenceHTML.includes('textHWQ_U/,')) {
  throw new Error('compact text fence leaked the info prefix into rendered code');
}

const normalFenceHTML = context.renderMarkdown('```text\nHWQ_U/,\n```');
assertIncludes(normalFenceHTML, 'HWQ_U/,');

const openCompactFenceHTML = context.renderMarkdown('```textHWQ_U/,前端在渲染时还是会出现这样的小问题');
assertIncludes(openCompactFenceHTML, '<pre');
assertIncludes(openCompactFenceHTML, 'HWQ_U/,前端在渲染时还是会出现这样的小问题');
if (openCompactFenceHTML.includes('textHWQ_U/,')) {
  throw new Error('open compact text fence leaked the info prefix into rendered code');
}

function assertIncludes(value, expected) {
  if (!value.includes(expected)) {
    throw new Error(`expected rendered Markdown to include ${expected}`);
  }
}
