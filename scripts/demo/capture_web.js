// capture_web.js — web UI screenshots for docs/, driven by scripts/demo/screenshots.sh
// against the synthetic fixture it builds. Not meant to run standalone.
//
// Usage: node capture_web.js <admin-port> <docs-dir>
const { chromium } = require('playwright');

async function waitForReady(page) {
  await page.waitForFunction(
    () => document.querySelectorAll('#tunnel-tbody tr.data-row').length > 0,
    { timeout: 15000 }
  );
}

async function main() {
  const [, , adminPort, docsDir] = process.argv;
  const baseURL = `http://127.0.0.1:${adminPort}/`;

  const browser = await chromium.launch();
  const page = await browser.newPage({ viewport: { width: 1400, height: 900 } });

  await page.goto(baseURL);
  await waitForReady(page);
  await page.click('tr.data-row[data-name="db-primary"]'); // expand its traffic chart
  await page.waitForSelector('tr.graph-row[data-name="db-primary"] canvas');
  await page.waitForTimeout(10000); // let enough SSE ticks land for a real waveform, not a flat line
  await page.screenshot({ path: `${docsDir}/ui-status.png` });

  await page.evaluate(() => window.switchTab('patterns'));
  await page.waitForTimeout(500);
  await page.screenshot({ path: `${docsDir}/ui-rules.png` });

  await page.evaluate(() => window.switchTab('logs'));
  await page.waitForFunction(
    () => document.querySelectorAll('.log-line').length > 0,
    { timeout: 10000 }
  ).catch(() => {}); // best-effort — screenshot either way
  await page.waitForTimeout(500);
  await page.screenshot({ path: `${docsDir}/ui-logs.png` });

  await page.evaluate(() => window.switchTab('settings'));
  await page.waitForTimeout(300);
  await page.screenshot({ path: `${docsDir}/ui-settings.png` });

  await browser.close();
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
