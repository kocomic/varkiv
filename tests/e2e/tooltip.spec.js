const { test, expect, devices } = require('@playwright/test');

const tooltip = page => page.locator('#ui-tooltip');
const systemTheme = page => page.locator('.theme-switch [data-theme-mode="system"]');

const contrast = (first, second) => {
  const channels = color => color.match(/[\d.]+/g).slice(0, 3).map(Number).map(value => {
    const normalized = value / 255;
    return normalized <= .04045 ? normalized / 12.92 : ((normalized + .055) / 1.055) ** 2.4;
  });
  const luminance = color => {
    const [red, green, blue] = channels(color);
    return .2126 * red + .7152 * green + .0722 * blue;
  };
  const [lighter, darker] = [luminance(first), luminance(second)].sort((a, b) => b - a);
  return (lighter + .05) / (darker + .05);
};

async function openLibrary(page, query = 'tooltip-e2e') {
  await page.goto(`/?${query}#library`, { waitUntil: 'domcontentloaded' });
  await expect(tooltip(page)).toHaveCount(1);
  await expect(systemTheme(page)).toHaveAttribute('data-tooltip', /.*/);
}

async function installHintTrigger(page, parentSelector, text, style = '') {
  await page.evaluate(({ parentSelector, text, style }) => {
    const trigger = document.createElement('button');
    trigger.type = 'button';
    trigger.id = 'tooltip-e2e-hint';
    trigger.className = 'hint-trigger';
    trigger.dataset.tooltip = text;
    trigger.setAttribute('aria-label', text);
    trigger.style.cssText = style;
    trigger.innerHTML = '<span aria-hidden="true">?</span>';
    document.querySelector(parentSelector).append(trigger);
  }, { parentSelector, text, style });
  return page.locator('#tooltip-e2e-hint');
}

test('real import path tooltip is readable in both themes and works with hover and keyboard focus', async ({ page }) => {
  await page.goto('/?tooltip-inputs#sources', { waitUntil: 'domcontentloaded' });
  await expect(tooltip(page)).toHaveCount(1);
  const trigger = page.locator('#sources-view .hint-trigger');
  const copy = '填写相对 /library 的路径；NAS 目录需先挂载到容器的 /library。';

  await expect(trigger).not.toHaveAttribute('title', /.+/);
  for (const theme of ['dark', 'light']) {
    await page.locator(`[data-theme-mode="${theme}"]`).click();
    await page.keyboard.press('Escape');
    await page.mouse.move(2, 300);
    await trigger.hover();
    await expect(tooltip(page)).toBeVisible();
    await expect(tooltip(page)).toHaveText(copy);
    const visual = await tooltip(page).evaluate(element => {
      const style = getComputedStyle(element);
      const rect = element.getBoundingClientRect();
      return {
        foreground: style.color,
        background: style.backgroundColor,
        fontSize: Number.parseFloat(style.fontSize),
        clipped: element.scrollWidth > element.clientWidth + 1 || element.scrollHeight > element.clientHeight + 1,
        inViewport: rect.left >= 0 && rect.top >= 0 && rect.right <= innerWidth && rect.bottom <= innerHeight,
      };
    });
    expect(contrast(visual.foreground, visual.background), `${theme} tooltip contrast`).toBeGreaterThanOrEqual(4.5);
    expect(visual.fontSize).toBeGreaterThanOrEqual(14);
    expect(visual.clipped).toBe(false);
    expect(visual.inViewport).toBe(true);
    await page.mouse.move(2, 300);
    await expect(tooltip(page)).toBeHidden();
  }

  await trigger.focus();
  await expect(tooltip(page)).toBeVisible();
  await expect(tooltip(page)).toHaveText(copy);
  await expect(trigger).toHaveAttribute('aria-describedby', /(^|\s)ui-tooltip(\s|$)/);
  await page.keyboard.press('Escape');
  await expect(tooltip(page)).toBeHidden();
  await expect(trigger).toBeFocused();
});

test('tooltip text follows all four interface locales', async ({ page }) => {
  await openLibrary(page, 'tooltip-locales');
  const expected = [
    ['zh-CN', '跟随系统'],
    ['zh-TW', '跟隨系統'],
    ['ja', 'システム設定に従う'],
    ['en', 'Use system setting'],
  ];

  for (const [locale, label] of expected) {
    await page.mouse.move(1, 300);
    await page.locator('#locale').selectOption(locale);
    await systemTheme(page).hover();
    await expect(tooltip(page)).toBeVisible();
    await expect(tooltip(page)).toHaveText(label);
    await page.keyboard.press('Escape');
    await expect(tooltip(page)).toBeHidden();
  }
});

test('a touch hint trigger toggles without changing another action', async ({ browser, baseURL }) => {
  const context = await browser.newContext({ ...devices['Pixel 5'], baseURL });
  const page = await context.newPage();
  try {
    await page.goto('/?tooltip-touch#sources', { waitUntil: 'domcontentloaded' });
    await expect(tooltip(page)).toHaveCount(1);
    const trigger = page.locator('#sources-view .hint-trigger');
    const box = await trigger.boundingBox();
    expect(box.width).toBeGreaterThanOrEqual(44);
    expect(box.height).toBeGreaterThanOrEqual(44);

    for (const theme of ['dark', 'light']) {
      await page.locator(`[data-theme-mode="${theme}"]`).tap();
      await page.keyboard.press('Escape');
      await trigger.tap();
      await expect(tooltip(page)).toBeVisible();
      await expect(tooltip(page)).toHaveText('填写相对 /library 的路径；NAS 目录需先挂载到容器的 /library。');
      await expect(trigger).toHaveAttribute('aria-expanded', 'true');
      const visual = await tooltip(page).evaluate(element => {
        const style = getComputedStyle(element);
        const rect = element.getBoundingClientRect();
        return {
          foreground: style.color,
          background: style.backgroundColor,
          fontSize: Number.parseFloat(style.fontSize),
          clipped: element.scrollWidth > element.clientWidth + 1 || element.scrollHeight > element.clientHeight + 1,
          inViewport: rect.left >= 0 && rect.top >= 0 && rect.right <= innerWidth && rect.bottom <= innerHeight,
          overflow: document.documentElement.scrollWidth - document.documentElement.clientWidth,
        };
      });
      expect(contrast(visual.foreground, visual.background), `${theme} mobile tooltip contrast`).toBeGreaterThanOrEqual(4.5);
      expect(visual.fontSize).toBeGreaterThanOrEqual(14);
      expect(visual.clipped).toBe(false);
      expect(visual.inViewport).toBe(true);
      expect(visual.overflow).toBe(0);

      await page.touchscreen.tap(8, 260);
      await expect(tooltip(page)).toBeHidden();
      await expect(trigger).toHaveAttribute('aria-expanded', 'false');
    }
  } finally {
    await context.close();
  }
});

test('a tooltip opened inside a native dialog remains in the top layer', async ({ page }) => {
  await page.goto('/?tooltip-dialog#platforms', { waitUntil: 'domcontentloaded' });
  await page.locator('#new-custom-platform').click();
  await expect(page.locator('#platform-editor-dialog')).toHaveAttribute('open', '');
  const trigger = await installHintTrigger(page, '#platform-editor-dialog form', '对话框帮助');

  await trigger.focus();
  await expect(tooltip(page)).toBeVisible();
  const layer = await tooltip(page).evaluate(element => {
    const rect = element.getBoundingClientRect();
    const x = rect.left + Math.min(12, rect.width / 2);
    const y = rect.top + Math.min(12, rect.height / 2);
    const hit = document.elementFromPoint(x, y);
    return {
      popover: element.matches(':popover-open'),
      fallback: element.classList.contains('tooltip-fallback-open'),
      hit: hit === element || element.contains(hit),
    };
  });
  expect(layer.popover || layer.fallback).toBeTruthy();
  expect(layer.hit).toBeTruthy();
});

test('edge-positioned tooltip stays inside a phone viewport without document overflow', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await openLibrary(page, 'tooltip-viewport');
  const trigger = await installHintTrigger(
    page,
    'body',
    '靠近视口边缘时仍应完整显示的说明文字',
    'position:fixed;right:1px;bottom:76px;z-index:40',
  );

  await trigger.focus();
  await expect(tooltip(page)).toBeVisible();
  const geometry = await tooltip(page).evaluate(element => {
    const rect = element.getBoundingClientRect();
    return {
      left: rect.left,
      top: rect.top,
      right: rect.right,
      bottom: rect.bottom,
      viewportWidth: innerWidth,
      viewportHeight: innerHeight,
      overflow: document.documentElement.scrollWidth - document.documentElement.clientWidth,
    };
  });
  expect(geometry.left).toBeGreaterThanOrEqual(0);
  expect(geometry.top).toBeGreaterThanOrEqual(0);
  expect(geometry.right).toBeLessThanOrEqual(geometry.viewportWidth);
  expect(geometry.bottom).toBeLessThanOrEqual(geometry.viewportHeight);
  expect(geometry.overflow).toBe(0);
});

test('primary workspaces expose no native hover title except the emulator iframe name', async ({ page }) => {
  for (const route of ['library', 'platforms', 'sources', 'packages', 'sync', 'settings']) {
    await page.goto(`/?tooltip-native-title-${route}#${route}`, { waitUntil: 'domcontentloaded' });
    await expect(page.locator('.view:visible')).toHaveCount(1);
    await expect(page.locator('[title]:not(iframe)')).toHaveCount(0);
  }

  await page.goto('/?tooltip-native-title-covers#library', { waitUntil: 'domcontentloaded' });
  await page.locator('[data-library-mode="covers"]').click();
  await expect(page.locator('[title]:not(iframe)')).toHaveCount(0);
});
