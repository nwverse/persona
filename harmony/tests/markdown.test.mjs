import assert from 'node:assert/strict';
import test from 'node:test';
import { markdownProseText, markdownCodeSelection, ConversationMarkdownCache, chatImagePath, markdownLinkProvider, markdownImagePaths, markdownMediaRuns, chatFilePath, markdownFilePath, structuredBubble, markdownInlineText, markdownTableWidths, markdownVisibleText, parseMarkdown, agentReplyBodySize, markdownProseRuns, recapGlyphSize } from '../entry/src/main/ets/core/Markdown.ts';
import { chatCodeRuns } from '../entry/src/main/ets/core/ChatCode.ts';
import { readFileSync } from 'node:fs';

test('Agent reply prose follows the reading metric at each Dynamic Type category and between categories', () => {
  const bodySizes = [14,15,16,17,19,21,23,28,33,40,47,53];
  const markdownSizes = [15,15,16,17,19,20,22,26,31,37,43,48];
  for (const [index,size] of bodySizes.entries()) {
    assert.ok(Math.abs(agentReplyBodySize(size/17) - markdownSizes[index]) < 0.001);
  }
  assert.equal(agentReplyBodySize(3.2),48);
  assert.equal(agentReplyBodySize(3.2,true),45.293);
  assert.ok(agentReplyBodySize(2)>31 && agentReplyBodySize(2)<37);
});

test('recap glyphs grow continuously in Latin and CJK at the native reading sizes', () => {
  for (const [han, regular, largest] of [[true, 16.4, 43.6], [false, 17, 42]]) {
    assert.equal(recapGlyphSize(17, han), regular);
    assert.equal(recapGlyphSize(48, han), largest);
    assert.ok(Math.abs(recapGlyphSize(32.5, han) - (regular + largest) / 2) < 0.001);
    assert.ok(recapGlyphSize(14, han) < regular);
  }
});

test('only Han and full-width punctuation use the narrower CJK fallback advance in reading prose', () => {
  const source = 'PR #42 看完了：max-age，名\u{e0100}字。 👩🏽‍💻 café';
  const runs = markdownProseRuns(source);
  assert.equal(runs.map(run => run.text).join(''),source);
  assert.deepEqual(runs.filter(run => run.han).map(run => run.text),['看完了：','，名\u{e0100}字。']);
});

function linkUrls(blocks, result = []) {
  for (const block of blocks) {
    for (const node of block.inlines) {
      if (node.kind === 'link') result.push(node.url);
      linkUrlsFromInline(node, result);
    }
    linkUrls(block.children, result);
    for (const item of block.items) linkUrls(item.children, result);
    for (const row of block.rows) {
      for (const cell of row.cells) {
        for (const node of cell) {
          if (node.kind === 'link') result.push(node.url);
          linkUrlsFromInline(node, result);
        }
      }
    }
  }
  return result;
}

function linkUrlsFromInline(node, result) {
  for (const child of node.children) {
    if (child.kind === 'link') result.push(child.url);
    linkUrlsFromInline(child, result);
  }
}

test('parses chat Markdown into readable semantic blocks without losing CJK text', () => {
  const blocks = parseMarkdown(
    '# 结果 Results\n\n这是 **粗体** 和 _斜体_，还有 `片段`。\n\n- 第一项\n- 二つ目\n\n> 引用内容\n\n---\n\n| 项目 | 值 |\n| --- | :---: |\n| 结果 | 完了 |',
  );

  assert.deepEqual(blocks.map((block) => block.kind), ['heading', 'paragraph', 'list', 'quote', 'rule', 'table']);
  assert.deepEqual(blocks[1].inlines.map((node) => node.kind), ['text', 'bold', 'text', 'italic', 'text', 'code', 'text']);
  assert.equal(blocks[2].items.length, 2);
  assert.equal(blocks[3].children[0].kind, 'paragraph');
  assert.equal(blocks[5].rows.length, 2);
  assert.equal(blocks[5].rows[0].header, true);
  const visible = markdownVisibleText(blocks);
  for (const value of ['结果 Results', '粗体', '斜体', '片段', '第一项', '二つ目', '引用内容', '项目', '完了']) {
    assert.ok(visible.includes(value), `missing visible text: ${value}`);
  }

  const oneColumn = parseMarkdown('| 状态 |\n| --- |\n| 完了 |');
  assert.equal(oneColumn[0].kind, 'table');
  assert.equal(oneColumn[0].rows.length, 2);
});

test('keeps fenced code verbatim and labels its language', () => {
  const code = 'const 你好 = "**still code**";\nconsole.log(你好);';
  const blocks = parseMarkdown('```ts\n' + code + '\n```');
  assert.equal(blocks.length, 1);
  assert.equal(blocks[0].kind, 'code');
  assert.equal(blocks[0].language, 'ts');
  assert.equal(blocks[0].text, code + '\n');
  assert.equal(markdownVisibleText(blocks), code + '\n');
});

test('retains ordered list numbers and nests indented list items', () => {
  const blocks = parseMarkdown('3. 开始\n   - 子項目\n4. 完了');
  assert.equal(blocks.length, 1);
  assert.equal(blocks[0].kind, 'list');
  assert.equal(blocks[0].ordered, true);
  assert.equal(blocks[0].items[0].marker, '3.');
  assert.equal(blocks[0].items[1].marker, '4.');
  assert.equal(blocks[0].items[0].children[1].kind, 'list');
  assert.ok(markdownVisibleText(blocks).includes('子項目'));
});

test('preserves malformed fences and unsupported inline syntax literally', () => {
  const unfinishedFence = '```js\nconst value = "still visible";';
  const fence = parseMarkdown(unfinishedFence);
  assert.equal(fence[0].kind, 'raw');
  assert.equal(fence[0].text, unfinishedFence);

  const unsupported = '~~not parsed~~ and *an unfinished emphasis';
  const inline = parseMarkdown(unsupported);
  assert.equal(inline[0].kind, 'paragraph');
  assert.equal(markdownVisibleText(inline), unsupported);
});

test('HTTP(S) links are clickable and external image syntax stays inert', () => {
  const source = '[safe](https://example.test/a?q=1) [run](javascript:alert(1)) [data](data:text/html,x) ![image](https://cdn.example.test/p.png) <script>https://evil.test</script>';
  const blocks = parseMarkdown(source);
  assert.deepEqual(linkUrls(blocks), ['https://example.test/a?q=1']);
  const visible = markdownVisibleText(blocks);
  assert.ok(visible.includes('[run](javascript:alert(1))'));
  assert.ok(visible.includes('[data](data:text/html,x)'));
  assert.ok(visible.includes('![image](https://cdn.example.test/p.png)'));
  assert.ok(visible.includes('<script>https://evil.test</script>'));
  assert.ok(visible.includes('safe'));
});

test('keeps bare HTTP(S) links clickable while preserving trailing punctuation', () => {
  const blocks = parseMarkdown('查看 https://example.test/path_(x). 然后结束。');
  assert.deepEqual(linkUrls(blocks), ['https://example.test/path_(x)']);
  assert.ok(markdownVisibleText(blocks).includes('https://example.test/path_(x).'));

  const cjk = parseMarkdown('访问 https://example.test/x。结束');
  assert.deepEqual(linkUrls(cjk), ['https://example.test/x']);
  assert.ok(markdownVisibleText(cjk).includes('https://example.test/x。结束'));
});


test('native fence styling preserves every character and keeps comment markers inside strings', () => {
  for (const [language, source] of [
    ['typescript', 'const url = "https://example.test/#id"; // 行尾\nreturn 42;\n'],
    ['json', '{"ready": true, "path": "a//b", "n": -2.5}\n'],
    ['python', 'def hello():\n    return "#文"  # note\n'],
    ['bash', 'if test "$VALUE"; then\n  echo "done"\nfi\n'],
    ['sql', "SELECT 'value' FROM entries -- result\n"],
    ['diff', '@@ -1 +1 @@\n-before\n+after\n'],
  ]) {
    const runs = chatCodeRuns(source, language);
    assert.equal(runs.map(r => r.text).join(''), source, language);
    let offset = 0;
    for (const run of runs) { assert.equal(run.offset, offset); offset += run.text.length; }
    assert.ok(runs.some(r => r.ink !== 'plain'), language);
  }
  const js = chatCodeRuns('const url = "https://site/#item"; // end', 'ts');
  assert.equal(js.find(r => r.ink === 'string').text, '"https://site/#item"');
  assert.equal(js.find(r => r.ink === 'comment').text, '// end');
});

test('all table rows keep the header columns, inline literals, explicit breaks and alignment', () => {
  const [table] = parseMarkdown('| 名称 | 状态 | 说明 |\n| :--- | :---: | ---: |\n| `a\\|b` | 完成 | 较长<br/>说明 |\n| 短行 | 等待 |\n| 多项 | 失败 | `<br>` | 忽略 |');
  assert.deepEqual(table.alignments, ['start', 'center', 'end']);
  assert.deepEqual(table.rows.map(row => row.cells.map(markdownInlineText)), [
    ['名称', '状态', '说明'], ['a|b', '完成', '较长\n说明'], ['短行', '等待', ''], ['多项', '失败', '<br>'],
  ]);
  assert.equal(table.rows[1].cells[0][0].kind, 'code');
});

test('short columns make room for prose while wide tables and large type keep a readable column floor', () => {
  const fitted = markdownTableWidths([30, 33, 420], 310, 1);
  assert.ok(Math.abs(fitted.reduce((a,b) => a+b) - 310) < 0.001);
  assert.ok(fitted[2] > 190 && fitted[0] < 60);
  const wide = markdownTableWidths([92, 88, 110, 128, 80], 350, 1);
  assert.ok(wide.every(width => width >= 94));
  assert.ok(wide.reduce((a,b) => a+b) > 400);
  const large = markdownTableWidths([130, 160, 370], 320, 2);
  assert.ok(large.reduce((a,b) => a+b) > 320);
});

test('unknown and large fences use one selectable run without truncating code', () => {
  for (const [source, lang] of [['**raw**\n', 'future-lang'], ['x'.repeat(20000), 'json'], ['const n = 1;\n'.repeat(900), 'ts']]) {
    assert.deepEqual(chatCodeRuns(source, lang), [{ offset: 0, ink: 'plain', text: source }]);
  }
});

test('conversation code and diff inks retain 4.5:1 contrast on the native code surface in both modes', () => {
  function luminance(hex) {
    const rgb=[1,3,5].map(i => parseInt(hex.slice(i,i+2),16)/255).map(n => n<=0.04045?n/12.92:((n+0.055)/1.055)**2.4);
    return rgb[0]*0.2126+rgb[1]*0.7152+rgb[2]*0.0722;
  }
  for (const mode of ['base','dark']) {
    const path=new URL('../entry/src/main/resources/'+mode+'/element/color.json',import.meta.url);
    const values=Object.fromEntries(JSON.parse(readFileSync(path)).color.map(item=>[item.name,luminance(item.value)]));
    for (const role of ['ink','secondary','code_keyword','code_string','code_literal','positive','danger']) {
      const ink=values['persona_'+role], bg=values.persona_grouped;
      assert.ok((Math.max(ink,bg)+0.05)/(Math.min(ink,bg)+0.05)>=4.5,mode+' '+role);
    }
  }
});

test('Copy on an abbreviated tool output writes the full original plain text', async () => {
  const source = readFileSync(new URL('../entry/src/main/ets/components/ConversationCode.ets', import.meta.url), 'utf8');
  const start = source.indexOf('  private async copyCode()'), end = source.indexOf('  @Builder body()', start);
  assert.ok(start >= 0 && end > start);
  const { stripTypeScriptTypes } = await import('node:module');
  const writes = [], notices = [];
  const board = { MIMETYPE_TEXT_PLAIN: 'text/plain', createData: (type, value) => ({ type, value }),
    getSystemPasteboard: () => ({ setData: async item => { writes.push(item); } }) };
  const view = new Function('pasteboard','tr','return '+stripTypeScriptTypes('class Copy {'+source.slice(start,end)+'}'))(board,key=>key);
  const code = new view(); code.code = 'output\n'.repeat(700); code.prefix = 'output…';
  code.getUIContext = () => ({getPromptAction: () => ({showToast: item => notices.push(item.message)})});
  await code.copyCode();
  assert.deepEqual(writes,[{type:'text/plain',value:code.code}]);
  assert.deepEqual(notices,['ev_code_copied']);
});

test('long quote bubbles take a bounded column and file references keep their readable label and workspace destination', () => {
  assert.equal(structuredBubble('解释一下'), false);
  assert.equal(structuredBubble('> a long message\n\n解释一下'), true);
  assert.equal(structuredBubble('> one quote'), true);
  const file = '.claude/worktrees/review/internal/personalim/service.go';
  const md = parseMarkdown('在 [service.go:637](' + file + ':637) 中。');
  assert.equal(markdownVisibleText(md), '在 service.go:637 中。');
  const node = md[0].inlines.find(n => n.kind === 'file');
  assert.equal(node.url, file + ':637');
  assert.equal(chatFilePath(node.url,'/work/project/'), '/work/project/' + file);
  assert.equal(markdownFilePath('a.ts#L20-L22'), 'a.ts');
  assert.equal(chatFilePath('/work/project/a.ts:6:4','/work/project'), '/work/project/a.ts');
  for (const dest of ['../other.ts','/work/project-e/a.ts','//host/a.ts','file:///x.ts','javascript:foo.ts','src/%2e%2e/secret.ts']) {
    assert.equal(chatFilePath(dest,'/work/project'), '');
  }
});

test('a Codex answer keeps its embedded screenshot in place and its image links inline', () => {
  const base='/Users/u/.codex-work/visualizations/2026/11/22/some-turn/';
  const source='已装到你的手机。\n\n![真机上的思考摘要标题]('+base+'heading.png)\n\n调用进度。[查看真机动图]('+base+'live.gif)\n\n[查看真机折叠对照]('+base+'fold.png)';
  const blocks=parseMarkdown(source);
  const paths=markdownImagePaths(blocks,'/Users/u/work/project');
  assert.deepEqual(paths,[base+'heading.png',base+'live.gif',base+'fold.png']);
  assert.equal(markdownMediaRuns(blocks[1].inlines,'/repo')[0].label,'真机上的思考摘要标题');
  assert.equal(markdownMediaRuns(blocks[2].inlines,'/repo').at(-1).nodes.at(-1).url,base+'live.gif');
  assert.equal(blocks[2].inlines.at(-1).kind,'file');assert.equal(blocks[3].inlines[0].kind,'file');
  assert.equal(markdownVisibleText(blocks).includes(base),false);
  assert.equal(chatImagePath(base+'boe-sheet-download/step2.jpg','/repo'),base+'boe-sheet-download/step2.jpg');
  for (const path of ['nested/../outside.jpg','nested/%2e%2e/outside.jpg','/outside.jpg']) {
    assert.equal(chatImagePath(base+path,'/repo'),'');
  }
});
test('mixed prose, formatted labels and repeated images keep their order; code examples never load images', () => {
  const blocks=parseMarkdown('前 **加粗 ![图](fig/a%20b.PNG) 结尾** 后 `![例](ignore.png)`\n\n![图](<fig/a b.PNG>)\n\n```md\n![例](ignore2.png)\n```');
  const runs=markdownMediaRuns(blocks[0].inlines,'/repo');
  assert.deepEqual(runs.map(r=>r.path||markdownInlineText(r.nodes)),['前 加粗 ','/repo/fig/a b.PNG',' 结尾 后 ![例](ignore.png)']);
  assert.deepEqual(markdownImagePaths(blocks,'/repo'),['/repo/fig/a b.PNG']);
  for(const target of ['../x.png','/tmp/secret.png','file:///x.png','//host/x.png','https://host/x.png','%2e%2e/x.png','bad.svg','/tmp/.codex/visualizations/bad/x.png']){
    assert.equal(chatImagePath(target,'/repo'),'');
  }
});

test('paragraph citations keep adjacent labels, separators and destinations in the text flow', () => {
  const block = parseMarkdown('已发布。 [发布记录](https://github.com/x/y/releases) · [接入说明](https://example.org/docs)')[0];
  const runs = markdownMediaRuns(block.inlines, '/repo');
  assert.equal(runs.length, 1);
  assert.equal(markdownInlineText(runs[0].nodes), '已发布。 发布记录 · 接入说明');
  assert.deepEqual(runs[0].nodes.filter(n => n.kind === 'link').map(n => n.url),
    ['https://github.com/x/y/releases', 'https://example.org/docs']);
});
test('provider marks use actual host boundaries and keep unrecognized sites plain', () => {
  for (const [host, brand] of [['GITHUB.com','github'], ['gist.github.com','github'], ['www.figma.com','figma'],
    ['linear.app','linear'], ['team.notion.so','notion'], ['app.notion.com','notion'], ['sheets.google.com','googledrive']]) {
    assert.equal(markdownLinkProvider('https://' + host + '/example'), brand);
  }
  for (const url of ['https://github.com.example.org/x','https://notgithub.com','https://github.com@evil.example/x',
    'https://other.org/?host=github.com','javascript:github.com','https://unknown.example']) assert.equal(markdownLinkProvider(url), undefined);
  const text = parseMarkdown('[GitHub](https://github.com) 与 `abc`')[0].inlines;
  assert.equal(markdownCodeSelection(text,0,13,true),'GitHub 与 abc');
});
test('screen captures exported to the workspace owner’s tmp directory keep an image link and gallery entry', () => {
  const file = '/Users/u/tmp/persona-conversation-follow-20261123.jpg', cwd = '/Users/u/workspace/persona';
  const blocks = parseMarkdown('检查通过。 [查看四个状态的预览对照](' + file + ')');
  assert.deepEqual(markdownImagePaths(blocks, cwd), [file]);
  const ref = markdownMediaRuns(blocks[0].inlines, cwd)[0].nodes.at(-1);
  assert.equal(ref.kind, 'file'); assert.equal(chatImagePath(ref.url,cwd),file);
  assert.equal(markdownMediaRuns(parseMarkdown('![预览](' + file + ')')[0].inlines,cwd)[0].path, file);
  for (const path of ['/Users/other/tmp/x.jpg','/Users/u/tmpx/x.jpg','/Users/u/tmp/../x.jpg','/Users/u/tmp/%2e%2e/x.jpg']) {
    assert.equal(chatImagePath(path,cwd),'');
  }
});

test('a chat reuses previously parsed Markdown on return and bounds retained documents',()=>{
 const cache=new ConversationMarkdownCache(), first=cache.parse('如何阅读？');
 assert.equal(cache.parse('如何阅读？'),first);
 for(let i=0;i<32;i++) cache.parse('turn '+i);
 assert.notEqual(cache.parse('如何阅读？'),first);
 const a='a'.repeat(70000), b='b'.repeat(70000), old=cache.parse(a);
 cache.parse(b);assert.notEqual(cache.parse(a),old);
});

test('copying across code omits the room beside CJK letters and preserves spaces in the source', () => {
  const parts=parseMarkdown('值是`a\u2009b`。')[0].inlines;
  assert.deepEqual([parts[1].gapBefore,parts[1].gapAfter],[true,false]);
  assert.equal(markdownCodeSelection(parts,0,7),'值是a\u2009b。');
  assert.equal(markdownCodeSelection(parts,3,6),undefined);
  const spaced=parseMarkdown('值是 `a\u2009b`。')[0].inlines;
  assert.deepEqual([spaced[1].gapBefore,spaced[1].gapAfter],[false,false]);
  assert.equal(markdownCodeSelection(spaced,0,7),undefined);
});

test('inline code gets room only where it meets a CJK letter directly', () => {
  const gaps=(source)=>{
    const found=[];
    const walk=(nodes)=>nodes.forEach(node=>{ if(node.kind==='code') found.push([node.text,node.gapBefore,node.gapAfter]); walk(node.children); });
    walk(parseMarkdown(source)[0].inlines);
    return found;
  };
  assert.deepEqual(gaps('在`internal/workflow`中'),[['internal/workflow',true,true]]);
  assert.deepEqual(gaps('改为`a`，然后`b`。'),[['a',true,false],['b',true,false]]);
  assert.deepEqual(gaps('**更新**`x`和_说明_'),[['x',true,true]]);
  assert.deepEqual(gaps('「`x`」・`y`ー'),[['x',false,false],['y',false,false]]);
  assert.deepEqual(gaps('한국어`x`かな`y`'),[['x',true,true],['y',true,false]]);
  assert.deepEqual(gaps('Use `x` and `y`.'),[['x',false,false],['y',false,false]]);
  assert.deepEqual(gaps('`a`中`b`'),[['a',false,true],['b',true,false]]);
  const table=parseMarkdown('| 名称 |\n| --- |\n| 值`v`说明 |')[0];
  const cell=table.rows[1].cells[0].find(node=>node.kind==='code');
  assert.deepEqual([cell.gapBefore,cell.gapAfter],[true,true]);
});

test('Claude list labels retain emphasis around inline code and stop before the explanation', () => {
  const source='- **单 IP 授权查询限制（项 32）：** 在统计期间。\n- **银行账户查询（项 36，`/account/e/info`）：** 约 95 天。';
  const [first,second]=parseMarkdown(source)[0].items.map(item=>item.children[0].inlines);
  assert.deepEqual(first.map(n=>n.kind),['bold','text']);
  assert.deepEqual(second.map(n=>n.kind),['bold','text']);
  assert.deepEqual(second[0].children.map(n=>n.kind),['text','code','text']);
  assert.equal(second[0].children[1].text,'/account/e/info');
});

test('slash commands stay together in prose and selections keep the original text', () => {
  const text='检查 231 上的新 /ai 以确认。', rendered=markdownProseText(text);
  assert.equal(rendered,text.replace('/ai','/\u2060ai'));
  for (const literal of ['/tmp/files','https://example.com/x','v1.0.1-ui','/ai\u2060b']) {
    assert.equal(markdownProseText(literal),literal);
  }
  const source=text+'（`bf118c9908`）。';
  const nodes=parseMarkdown(source)[0].inlines;
  const drawn=rendered+'（bf118c9908）。';
  assert.equal(markdownCodeSelection(nodes,0,drawn.length,true),text+'（bf118c9908）。');
  const at=rendered.indexOf('/');
  assert.equal(markdownCodeSelection(nodes,at,at+4,true),'/ai');
  assert.equal(markdownCodeSelection(nodes,at+1,at+2,true),'');
});
