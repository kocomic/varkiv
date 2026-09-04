const { test, expect } = require('@playwright/test');

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

async function openLibrary(page, colorScheme) {
  await page.emulateMedia({ colorScheme });
  await page.goto('/?theme-e2e#library', { waitUntil: 'domcontentloaded' });
  await expect(page.locator('html')).toHaveAttribute('data-theme-mode', 'system');
}

test('theme follows the system and reacts to preference changes', async ({ page }) => {
  await openLibrary(page, 'dark');
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
  await expect(page.locator('meta[name="theme-color"]')).toHaveAttribute('content', '#0b0c10');

  await page.emulateMedia({ colorScheme: 'light' });
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'light');
  await expect(page.locator('meta[name="theme-color"]')).toHaveAttribute('content', '#f5f6f8');
});

test('explicit theme persists and can return to system mode', async ({ page }) => {
  await openLibrary(page, 'light');
  await page.locator('.theme-switch [data-theme-mode="dark"]').click();
  await expect(page.locator('html')).toHaveAttribute('data-theme-mode', 'dark');
  await expect(page.locator('.theme-switch [data-theme-mode="dark"]')).toHaveAttribute('aria-pressed', 'true');
  await page.reload({ waitUntil: 'domcontentloaded' });
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');

  await page.locator('.theme-switch [data-theme-mode="system"]').click();
  await expect(page.locator('html')).toHaveAttribute('data-theme-mode', 'system');
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'light');
});

test('theme controls localize and remain usable on a phone', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await openLibrary(page, 'light');
  await page.locator('#locale').selectOption('en');
  await expect(page.locator('.theme-switch')).toHaveAttribute('aria-label', 'Appearance');
  await expect(page.locator('.theme-switch [data-theme-mode="system"]')).toHaveAttribute('data-tooltip', 'Use system setting');
  await expect(page.locator('.theme-switch [data-theme-mode="light"]')).toHaveAttribute('aria-label', 'Light mode');
  await expect(page.locator('.theme-switch [data-theme-mode="dark"]')).toHaveAttribute('aria-label', 'Dark mode');

  const geometry = await page.evaluate(() => ({
    viewport: innerWidth,
    document: document.documentElement.scrollWidth,
    topbar: document.querySelector('.topbar').getBoundingClientRect().height,
    buttons: [...document.querySelectorAll('.theme-switch button')].map(button => button.getBoundingClientRect().width)
  }));
  expect(geometry.document).toBe(geometry.viewport);
  expect(geometry.topbar).toBeGreaterThanOrEqual(108);
  expect(geometry.buttons.every(width => width >= 32)).toBeTruthy();
});

for (const colorScheme of ['light', 'dark']) {
  test(`${colorScheme} theme keeps representative text contrast readable`, async ({ page }) => {
    await openLibrary(page, colorScheme);
    const pairs = await page.evaluate(() => {
      const pair = selector => {
        const element = document.querySelector(selector);
        const style = getComputedStyle(element);
        let parent = element;
        let background = style.backgroundColor;
        while (parent && (background === 'rgba(0, 0, 0, 0)' || background === 'transparent')) {
          parent = parent.parentElement;
          background = parent ? getComputedStyle(parent).backgroundColor : getComputedStyle(document.body).backgroundColor;
        }
        return [style.color, background];
      };
      return {
        heading: pair('.page-head h1'),
        intro: pair('.intro'),
        search: pair('#search'),
        primary: pair('#new-game')
      };
    });
    for (const [name, [foreground, background]] of Object.entries(pairs)) {
      expect(contrast(foreground, background), `${name}: ${foreground} on ${background}`).toBeGreaterThanOrEqual(4.5);
    }
    await page.locator('a[href="#sources"]').click();
    await expect(page.locator('.identity-exchange-head .eyebrow')).toBeVisible();
    const identityLabel = await page.locator('.identity-exchange-head .eyebrow').evaluate(element => {
      const style = getComputedStyle(element);
      return [style.color, style.backgroundColor];
    });
    expect(contrast(...identityLabel), `identity label: ${identityLabel[0]} on ${identityLabel[1]}`).toBeGreaterThanOrEqual(4.5);
  });
}

for (const colorScheme of ['light', 'dark']) {
  test(`${colorScheme} theme keeps every primary route on readable neutral surfaces`, async ({ page }) => {
    await openLibrary(page, colorScheme);
    const routes = [
      ['library', '.stats'],
      ['platforms', '.platform-row'],
      ['sources', '#sources-view .flow-card'],
      ['packages', '#packages-view .flow-card'],
      ['sync', '.sync-panel'],
      ['settings', '.settings-overview article']
    ];
    for (const [route, selector] of routes) {
      await page.locator(`a[href="#${route}"]`).click();
      const surface = page.locator(selector).first();
      await expect(surface).toBeVisible();
      const colors = await surface.evaluate(element => {
        const style = getComputedStyle(element);
        return { foreground: style.color, background: style.backgroundColor };
      });
      expect(contrast(colors.foreground, colors.background), `${route}: ${colors.foreground} on ${colors.background}`).toBeGreaterThanOrEqual(4.5);
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth - innerWidth);
      expect(overflow, `${route} horizontal overflow`).toBe(0);
    }
  });
}

test('light theme uses white data surfaces on a cool-gray canvas without dark-theme ink shadows', async ({ page }) => {
  await openLibrary(page, 'light');
  const visual = await page.evaluate(() => {
    const style = selector => getComputedStyle(document.querySelector(selector));
    return {
      canvas: style('body').backgroundColor,
      titleShadow: style('.page-head h1').textShadow,
      sidebarShadow: style('.sidebar').boxShadow,
      statsBackground: style('.stats').backgroundColor,
      statsShadow: style('.stats').boxShadow,
      primaryBackground: style('#new-game').backgroundColor,
      eyebrowBackground: style('.eyebrow').backgroundColor,
      titleMark: getComputedStyle(document.querySelector('.page-head h1'), '::after').color,
      disclosureBackground: getComputedStyle(document.querySelector('.page-context-disclosure summary'), '::before').backgroundColor,
      disclosureShadow: getComputedStyle(document.querySelector('.page-context-disclosure summary'), '::before').boxShadow,
      semantic: ['--theme-purple', '--theme-lime', '--theme-red', '--theme-cyan'].map(name => style('html').getPropertyValue(name).trim())
    };
  });

  expect(visual.canvas).toBe('rgb(245, 246, 248)');
  expect(visual.titleShadow).toBe('none');
  expect(visual.sidebarShadow).not.toContain('rgb(5, 6, 9)');
  expect(visual.statsBackground).toBe('rgb(247, 248, 250)');
  expect(visual.statsShadow).not.toContain('6px 6px 0px');
  expect(visual.primaryBackground).toBe('rgb(91, 79, 196)');
  expect(visual.eyebrowBackground).toBe(visual.primaryBackground);
  expect(visual.titleMark).toBe(visual.primaryBackground);
  expect(visual.disclosureBackground).toBe('rgb(243, 244, 246)');
  expect(visual.disclosureShadow).not.toContain('rgb(5, 6, 9)');
  expect(new Set(visual.semantic).size).toBe(visual.semantic.length);
});

for (const colorScheme of ['light', 'dark']) {
  test(`${colorScheme} theme keeps dense operational copy readable inside every workbench`, async ({ page }) => {
    await openLibrary(page, colorScheme);
    const routes = [
      ['platforms', ['.platform-page-summary strong', '.platform-identity h3', '.platform-runtime-summary>strong']],
      ['sources', ['#sources-view .safety-note strong', '#sources-view .flow-card-head p', '#sources-view .storage-policy legend', '#sources-view .flow-rail span:not(.active) b', '#sources-view .source-settings-disclosure strong', '#sources-view .field-label code']],
      ['packages', ['#packages-view .safety-note strong', '#packages-view .frontend-choice legend', '#packages-view .template-catalog>header strong']],
      ['sync', ['.agent-boundary', '.sync-panel-count', '.sync-process-disclosure>summary span']],
      ['settings', ['.runtime-total strong', '.support-level.catalogued', '.settings-overview small', '.support-readiness>header>span', '.support-gate.pending>header>em', '.support-gate.pending .support-gate-components span', '.support-readiness>footer', '.acceptance-local-badge', '.acceptance-file-picker small', '.acceptance-preview-button', '.storage-maintenance .eyebrow', '.storage-recovery-card>header>span']]
    ];

    for (const [route, selectors] of routes) {
      await page.locator(`a[href="#${route}"]`).click();
      for (const selector of selectors) {
        const locator = page.locator(selector).first();
        await expect(locator, `${route} ${selector}`).toBeAttached();
        const [foreground, background] = await locator.evaluate(element => {
          const foreground = getComputedStyle(element).color;
          let current = element;
          let background = 'transparent';
          const isOpaque = color => {
            if (color === 'transparent' || color === 'rgba(0, 0, 0, 0)') return false;
            const channels = color.match(/[\d.]+/g)?.map(Number) || [];
            return channels.length < 4 || channels[3] > .92;
          };
          while (current && !isOpaque(background)) {
            background = getComputedStyle(current).backgroundColor;
            current = current.parentElement;
          }
          return [foreground, background];
        });
        expect(contrast(foreground, background), `${route} ${selector}: ${foreground} on ${background}`).toBeGreaterThanOrEqual(4.5);
      }
    }
  });
}
