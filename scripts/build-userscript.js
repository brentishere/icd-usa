const esbuild = require('esbuild');
const fs = require('fs');
const path = require('path');

const packageJson = require(path.join(__dirname, '../package.json'));
const distDir = path.join(__dirname, '../dist');

const executorEntry = path.join(__dirname, '../src/scriptcat-engine/userscript-entry.ts');
const executorOut = path.join(distDir, `aegiscrawler-${packageJson.version}.user.js`);

const dispatcherEntry = path.join(__dirname, '../src/scriptcat-engine/dispatcher-entry.ts');
const dispatcherOut = path.join(distDir, `aegiscrawler-dispatcher-${packageJson.version}.user.js`);

const COMMON_GRANTS = `// @grant        GM_xmlhttpRequest
// @grant        GM_getValue
// @grant        GM_setValue
// Userscript managers (ScriptCat/Tampermonkey) refuse cross-origin
// GM_xmlhttpRequest without an @connect declaration. The runtime target is
// still constrained fail-closed by the trusted-origin allowlist checked on
// the task descriptor's serverUrl; @connect is only the manager-side gate.
// @connect      *
`;

async function buildExecutor() {
  const result = await esbuild.build({
    entryPoints: [executorEntry],
    bundle: true,
    write: false,
    format: 'iife',
    target: 'es2020',
    platform: 'browser',
    globalName: '__OpenCrawlerExecutor',
    footer: { js: '__OpenCrawlerExecutor.boot();' },
  });

  const header = `// ==UserScript==
// @name         AegisCrawler PageResearch Agent Executor
// @namespace    https://github.com/singhand-labs/AegisCrawler
// @version      ${packageJson.version}
// @description  Universal rule executor for AegisCrawler data collection
// @author       Singhand Labs <zy@singhand.com>
// @match        *://*/*
// @grant        GM_openInTab
${COMMON_GRANTS}// @grant        unsafeWindow
// @run-at       document-end
// @noframes
// ==/UserScript==

`;

  fs.mkdirSync(distDir, { recursive: true });
  fs.writeFileSync(executorOut, header + result.outputFiles[0].text, 'utf8');
  console.log(`Built: ${executorOut}`);
}

async function buildDispatcher() {
  const result = await esbuild.build({
    entryPoints: [dispatcherEntry],
    bundle: true,
    write: false,
    format: 'iife',
    target: 'es2020',
    platform: 'browser',
    // The dispatcher self-starts when its GM APIs exist; no global export and
    // no footer call needed.
    globalName: '__AegisCrawlerDispatcher',
    footer: { js: '' },
  });

  const header = `// ==UserScript==
// @name         AegisCrawler Dispatcher (Background Worker)
// @namespace    https://github.com/singhand-labs/AegisCrawler
// @version      ${packageJson.version}
// @description  Background task dispatcher: claims AegisCrawler tasks and opens executor tabs
// @author       Singhand Labs <zy@singhand.com>
// ScriptCat background script — runs continuously in the manager's
// background context; no @match needed.
// @background
${COMMON_GRANTS}// @grant        GM_openInTab
// @grant        GM_closeInTab
// ==/UserScript==

`;

  fs.mkdirSync(distDir, { recursive: true });
  fs.writeFileSync(dispatcherOut, header + result.outputFiles[0].text, 'utf8');
  console.log(`Built: ${dispatcherOut}`);
}

async function build() {
  await buildExecutor();
  await buildDispatcher();
}

build().catch((err) => {
  console.error(err);
  process.exit(1);
});
