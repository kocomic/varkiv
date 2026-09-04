const { test, expect } = require('@playwright/test');
const fs = require('node:fs');
const path = require('node:path');
const { version: expectedVersion } = require('../../package.json');

test.describe.configure({ mode: 'serial' });

test.beforeEach(async ({ page }) => {
  page.clientErrors = [];
  page.on('pageerror', error => page.clientErrors.push(`pageerror: ${error.message}`));
  page.on('console', message => {
    if (message.type() === 'error') page.clientErrors.push(`console: ${message.text()}`);
  });
});

test.afterEach(async ({ page }) => {
  expect(page.clientErrors, 'browser console and uncaught page errors').toEqual([]);
});

async function openTransfer(page) {
  await page.goto('/?e2e=import#sources');
  await expect(page.locator('#sources-view')).toBeVisible();
  await expect(page.locator('#import-platform-preset option[value="gba"]')).toHaveCount(1);
}

async function choosePlatform(page, platform = 'gba') {
  await page.locator('#import-platform-preset').selectOption(platform);
  await expect(page.locator('#import-platform-custom')).toHaveValue(platform);
}

test('runtime and browser assets share the canonical release version', async ({ page, request }) => {
  const health = await request.get('/api/v1/health');
  expect(health.ok()).toBeTruthy();
  await expect(health.json()).resolves.toMatchObject({ name: 'Varkiv', version: expectedVersion });

  await page.goto('/?e2e=version#library');
  await expect(page).toHaveTitle('Varkiv');
  await expect(page.locator('.brand strong')).toHaveText('VARKIV');
  await expect(page.locator('.brand small')).toHaveCount(0);
  await expect(page.locator('link[rel="icon"]')).toHaveAttribute('href', '/assets/favicon.svg');
  await expect.poll(() => page.locator('.brand-mark img').evaluate(image => image.complete && image.naturalWidth > 0)).toBe(true);
  await expect(page.locator('link[href^="/styles.css"]')).toHaveAttribute('href', `/styles.css?v=${expectedVersion}`);
  await expect(page.locator('link[href^="/theme.css"]')).toHaveAttribute('href', `/theme.css?v=${expectedVersion}`);
  await expect(page.locator('script[src^="/theme.js"]')).toHaveAttribute('src', `/theme.js?v=${expectedVersion}`);
  await expect(page.locator('script[src^="/i18n.js"]')).toHaveAttribute('src', `/i18n.js?v=${expectedVersion}`);
  await expect(page.locator('script[src^="/app.js"]')).toHaveAttribute('src', `/app.js?v=${expectedVersion}`);
  await expect.poll(() => page.evaluate(() => typeof canFallbackOriginalRaster)).toBe('function');
  expect(await page.evaluate(() => ({
    webp: canFallbackOriginalRaster('image/webp'),
    avif: canFallbackOriginalRaster(' IMAGE/AVIF; charset=binary '),
    bmp: canFallbackOriginalRaster('image/bmp'),
    icon: canFallbackOriginalRaster('image/vnd.microsoft.icon'),
    svg: canFallbackOriginalRaster('image/svg+xml'),
    html: canFallbackOriginalRaster('text/html'),
    missing: canFallbackOriginalRaster(''),
  }))).toEqual({ webp: true, avif: true, bmp: true, icon: true, svg: false, html: false, missing: false });
  await page.locator('#locale').selectOption('en');
  await expect(page).toHaveTitle('Varkiv');
  await expect(page.locator('.brand strong')).toHaveText('VARKIV');
  await expect(page.locator('.brand small')).toHaveCount(0);
});

test('settings exposes verified browser assets as a compact localized runtime status', async ({ page }) => {
  await page.route(/\/api\/v1\/web-emulation\/readiness(?:\?.*)?$/, route => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({
      enabled: true,
      mode: 'self-hosted-verified',
      same_origin: true,
      integrity_verified: true,
      emulatorjs_version: '4.2.3',
      assets_verified: 28,
      bytes_verified: 17161966,
      supported_platforms: ['2600', 'gamegear', 'gb', 'gba', 'gbc', 'mastersystem', 'megadrive', 'n64', 'nes', 'snes'],
      supported_extensions: ['.a26', '.bin', '.bs', '.fig', '.gb', '.gba', '.gbc', '.gen', '.gg', '.md', '.n64', '.nes', '.sfc', '.smc', '.smd', '.sms', '.unf', '.unif', '.v64', '.z64'],
      platform_capabilities: [
        { platform_id: '2600', core: 'stella2014', extensions: ['.a26', '.bin'], minimum_rom_bytes: 2048 },
        { platform_id: 'gamegear', core: 'genesis_plus_gx', extensions: ['.bin', '.gg'], minimum_rom_bytes: 8192 },
        { platform_id: 'gb', core: 'gambatte', extensions: ['.gb'], minimum_rom_bytes: 32768 },
        { platform_id: 'gba', core: 'mgba', extensions: ['.gba'], minimum_rom_bytes: 192 },
        { platform_id: 'gbc', core: 'gambatte', extensions: ['.gbc'], minimum_rom_bytes: 32768 },
        { platform_id: 'mastersystem', core: 'smsplus', extensions: ['.bin', '.sms'], minimum_rom_bytes: 8192 },
        { platform_id: 'megadrive', core: 'genesis_plus_gx', extensions: ['.bin', '.gen', '.md', '.smd'], minimum_rom_bytes: 512 },
        { platform_id: 'n64', core: 'mupen64plus_next', extensions: ['.n64', '.v64', '.z64'], minimum_rom_bytes: 4096 },
        { platform_id: 'nes', core: 'fceumm', extensions: ['.nes', '.unf', '.unif'], minimum_rom_bytes: 64 },
        { platform_id: 'snes', core: 'snes9x', extensions: ['.bs', '.fig', '.sfc', '.smc'], minimum_rom_bytes: 32768 },
      ],
    }),
  }));
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/?e2e=web-runtime-status#settings');
  const status = page.locator('#web-emulator-readiness');
  await expect(status).toBeVisible();

  for (const [locale, title, badge, fileText, platformText] of [
    ['zh-CN', '网页模拟器可用', '已核验', '28 个文件', '10 个平台'],
    ['zh-TW', '網頁模擬器可用', '已驗證', '28 個檔案', '10 個平台'],
    ['ja', 'Web エミュレーターを利用できます', '検証済み', '28 ファイル', '10 プラットフォーム'],
    ['en', 'Web emulator available', 'Verified', '28 files', '10 platforms'],
  ]) {
    await page.locator('#locale').selectOption(locale);
    await expect(page.locator('#web-emulator-readiness-title')).toHaveText(title);
    await expect(page.locator('#web-emulator-readiness-state')).toHaveText(badge);
    await expect(page.locator('#web-emulator-readiness-meta')).toContainText(fileText);
    await expect(page.locator('#web-emulator-readiness-meta')).toContainText(platformText);
    expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBe(0);
  }

  await page.locator('#locale').selectOption('zh-CN');
  await page.getByRole('button', { name: /Azahar \/ Nintendo 3DS/ }).click();
  await expect(page.locator('#runtime-evidence-scope')).toHaveText('Android 模拟器实测');
  await page.locator('#runtime-driver-fields details.runtime-advanced > summary').click();
  await expect(page.locator('[name="android_package"]')).toHaveValue('org.azahar_emu.azahar');
  await expect(page.locator('[name="android_package_candidates"]')).toHaveValue('io.github.lime3ds.android');
  await page.locator('#runtime-editor-dialog [data-close]').first().click();
});

test('platform runtime labels and playable formats come only from server readiness', async ({ page }) => {
  let enabled = true;
  await page.route(/\/api\/v1\/web-emulation\/readiness(?:\?.*)?$/, route => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({
      enabled,
      mode: enabled ? 'self-hosted-verified' : 'disabled',
      same_origin: enabled,
      integrity_verified: enabled,
      emulatorjs_version: enabled ? '4.2.3' : undefined,
      assets_verified: enabled ? 28 : 0,
      bytes_verified: enabled ? 17161966 : 0,
      supported_platforms: ['gba'],
      supported_extensions: ['.gba'],
      platform_capabilities: [{ platform_id: 'gba', core: 'mgba', extensions: ['.gba'], minimum_rom_bytes: 192 }],
    }),
  }));
  await page.goto('/?e2e=server-runtime-contract#platforms');
  const gba = page.locator('[data-platform-id="gba"]');
  const pokeMini = page.locator('[data-platform-id="pokemini"]');
  await expect(gba.locator('.runtime-badge')).toHaveText('浏览器可运行');
  await expect(pokeMini.locator('.runtime-badge')).toHaveText('仅外部 Web 方案');
  expect(await page.evaluate(() => ({ platforms: [...webPlayablePlatforms], extensions: [...webPlayableExtensions] }))).toEqual({ platforms: ['gba'], extensions: ['gba'] });
  expect(await page.evaluate(() => ({
    valid: Boolean(webPlayArtifact({ platform: 'gba' }, { artifacts: [{ path: 'game.gba', role: 'rom', sha256: 'a', size: 192 }] })),
    undersized: Boolean(webPlayArtifact({ platform: 'gba' }, { artifacts: [{ path: 'game.gba', role: 'rom', sha256: 'a', size: 191 }] })),
    mismatched: Boolean(webPlayArtifact({ platform: 'gba' }, { artifacts: [{ path: 'game.nes', role: 'rom', sha256: 'a', size: 192 }] })),
  }))).toEqual({ valid: true, undersized: false, mismatched: false });

  await page.locator('#locale').selectOption('en');
  await expect(gba.locator('.runtime-badge')).toHaveText('Browser ready');
  await expect(pokeMini.locator('.runtime-badge')).toHaveText('External web only');

  enabled = false;
  await page.reload();
  await expect(page.locator('[data-platform-id="gba"] .runtime-badge')).toHaveText('Browser disabled');
  await page.setViewportSize({ width: 390, height: 844 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBe(0);
});

test('active image formats never use the original-content rendering fallback', async ({ page }) => {
  let activeThumbnailRequests = 0;
  let passiveThumbnailRequests = 0;
  let activeOriginalRequests = 0;
  let passiveOriginalRequests = 0;
  await page.route('**/api/v1/media/active-svg/thumbnail?*', route => {
    activeThumbnailRequests += 1;
    return route.fulfill({ status: 415, contentType: 'application/json', body: '{"error":{"code":"media_thumbnail_unsupported"}}' });
  });
  await page.route('**/api/v1/media/active-svg/content', route => {
    activeOriginalRequests += 1;
    return route.fulfill({ status: 200, contentType: 'image/svg+xml', body: '<svg xmlns="http://www.w3.org/2000/svg"/>' });
  });
  await page.route('**/api/v1/media/passive-webp/thumbnail?*', route => {
    passiveThumbnailRequests += 1;
    return route.fulfill({ status: 415, contentType: 'application/json', body: '{"error":{"code":"media_thumbnail_unsupported"}}' });
  });
  await page.route('**/api/v1/media/passive-webp/content', route => {
    passiveOriginalRequests += 1;
    return route.fulfill({ status: 200, contentType: 'image/webp', body: 'passive-raster-fixture' });
  });

  await page.goto('/?e2e=media-fallback#library');
  expect(await page.evaluate(async () => {
    const active = await mediaImageBlob('active-svg', 128, 'image/svg+xml');
    const passive = await mediaImageBlob('passive-webp', 128, 'image/webp');
    return { active: active === null, passiveType: passive?.type, passiveSize: passive?.size };
  })).toEqual({ active: true, passiveType: 'image/webp', passiveSize: 22 });
  expect(activeThumbnailRequests).toBe(0);
  expect(passiveThumbnailRequests).toBe(0);
  expect(activeOriginalRequests).toBe(0);
  expect(passiveOriginalRequests).toBe(1);
  expect(page.clientErrors).toEqual([]);
});

test('six task-oriented workspaces are distinct and legacy routes remain compatible', async ({ page }) => {
  await page.goto('/?e2e=ia#library');
  const navigation = page.locator('.sidebar nav a[data-view]');
  await expect(navigation).toHaveCount(6);
  await expect(navigation).toHaveText(['资料库', '平台目录', '导入源', '整合包', '存档同步', '系统设置']);
  await expect(page.locator('#library-view h1')).toHaveText('资料库');

  await page.locator('a[href="#sources"]').click();
  await expect(page).toHaveURL(/#sources$/);
  await expect(page.locator('#sources-view')).toBeVisible();
  await expect(page.locator('#sources-view h1')).toHaveText('导入源');
  await expect(page.locator('#import-form')).toBeVisible();
  await expect(page.locator('#package-form')).toBeHidden();
  await expect(page.locator('#sources-view .source-registry')).toHaveCSS('border-radius', '2px');
  await expect(page.locator('#sources-view .import-layout')).toHaveCSS('border-radius', '2px');
  await expect(page.locator('#sources-view .flow-card').first()).toHaveCSS('box-shadow', 'none');
  await expect(page.locator('.source-settings-disclosure')).not.toHaveAttribute('open', '');
  await expect(page.locator('#source-name-field')).not.toBeVisible();
  const importKindPositions = await page.locator('.import-kind-option').evaluateAll(options => options.map(option => Math.round(option.getBoundingClientRect().x)));
  expect(new Set(importKindPositions).size).toBe(2);

  await page.locator('a[href="#packages"]').click();
  await expect(page).toHaveURL(/#packages$/);
  await expect(page.locator('#packages-view')).toBeVisible();
  await expect(page.locator('#packages-view h1')).toHaveText('整合包');
  await expect(page.locator('#package-form')).toBeVisible();
  await expect(page.locator('#import-form')).toBeHidden();

  await page.locator('a[href="#sync"]').click();
  await expect(page.locator('#devices-view')).toBeVisible();
  await expect(page.locator('#devices-view h1')).toHaveText('存档同步');
  await expect(page.locator('#device-form, #save-form, #devices-view input[type="file"]')).toHaveCount(0);

  await page.locator('a[href="#settings"]').click();
  await expect(page.locator('#settings-view')).toBeVisible();
  await expect(page.locator('#settings-view h1')).toHaveText('系统设置');
  await expect(page.locator('.settings-overview article')).toHaveCount(3);

  await page.locator('a[href="#platforms"]').click();
  await expect(page.locator('#platforms-view h1')).toHaveText('平台目录');
  await expect(page.locator('#platforms-view')).toBeVisible();
  await expect(page.locator('#platform-grid .platform-row')).not.toHaveCount(0);
  await expect(page.locator('#platform-grid .platform-row')).toHaveCount(72);
  await expect(page.locator('#platform-total')).toHaveText('72');
  await expect(page.locator('#platform-grid .platform-directory-head')).toBeVisible();
  await expect(page.locator('#platforms-view .platform-card')).toHaveCount(0);
  await expect(page.locator('.nav-item[href="#platforms"] svg')).toBeVisible();
  await expect(page.locator('.nav-item[href="#platforms"] use')).toHaveAttribute('href', '#icon-platforms');
  await expect(page.locator('.platform-mark').first()).toHaveCSS('clip-path', /polygon/);
  await expect(page.locator('.platform-mark svg [rx]')).toHaveCount(0);
  const ngpcPlatform = page.locator('#platform-grid [data-platform-id="ngpc"]');
  await expect(ngpcPlatform.locator('.platform-targets')).toContainText('RetroArch · Beetle NeoPop');
  await expect(ngpcPlatform.locator('.no-suggestion')).toHaveCount(0);
  const switchPlatform = page.locator('#platform-grid [data-platform-id="switch"]');
  await expect(switchPlatform.locator('.platform-targets')).toContainText('Eden');
  await expect(switchPlatform.locator('.platform-targets')).toContainText('暂无稳定建议');
  await expect(switchPlatform.locator('.no-suggestion')).toHaveCount(1);
  const xboxPlatform = page.locator('#platform-grid [data-platform-id="xbox"]');
  await expect(xboxPlatform.locator('.platform-targets')).toContainText('xemu');
  await expect(xboxPlatform).toHaveClass(/theme-microsoft/);
  const c64Platform = page.locator('#platform-grid [data-platform-id="c64"]');
  await expect(c64Platform.locator('.platform-targets')).toContainText('VICE x64sc');
  await expect(c64Platform).toHaveClass(/theme-computer/);
  const firstPlatformCompatibility = page.locator('#platform-grid .platform-compat').first();
  await expect(firstPlatformCompatibility).not.toHaveAttribute('open', '');
  await expect(firstPlatformCompatibility.locator('.platform-targets')).not.toBeVisible();
  await firstPlatformCompatibility.locator('summary').click();
  await expect(firstPlatformCompatibility.locator('.platform-targets')).toBeVisible();
  await expect(firstPlatformCompatibility.locator('.platform-targets svg')).toHaveCount(3);
  await expect(firstPlatformCompatibility.locator('svg [rx]')).toHaveCount(0);
  await firstPlatformCompatibility.locator('summary').click();
  await expect(firstPlatformCompatibility).not.toHaveAttribute('open', '');
  await page.setViewportSize({ width: 390, height: 844 });
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  expect(await page.locator('#platforms-view button').evaluateAll(buttons => buttons
    .filter(button => {
      const rect = button.getBoundingClientRect();
      return getComputedStyle(button).display !== 'none' && rect.width > 0 && rect.height > 0 && rect.height < 38;
    })
    .map(button => ({ text: button.textContent.trim(), height: button.getBoundingClientRect().height })))).toEqual([]);
  expect(await page.locator('#platform-grid .platform-row').evaluateAll(rows => rows
    .filter(row => row.scrollWidth > row.clientWidth + 1)
    .map(row => ({ width: row.clientWidth, scrollWidth: row.scrollWidth })))).toEqual([]);
  await page.setViewportSize({ width: 1280, height: 720 });

  const legacyRoutes = [
    ['transfer', '#sources-view'],
    ['devices', '#devices-view'],
    ['platforms', '#platforms-view']
  ];
  for (const [hash, selector] of legacyRoutes) {
    await page.goto(`/?e2e=legacy#${hash}`);
    await expect(page.locator(selector)).toBeVisible();
  }
});

test('long explanations stay behind progressive disclosure without hiding action warnings', async ({ page }) => {
  const views = [
    ['library', '#library-view'],
    ['platforms', '#platforms-view'],
    ['sources', '#sources-view'],
    ['packages', '#packages-view'],
    ['sync', '#devices-view'],
    ['settings', '#settings-view'],
  ];
  for (const [route, view] of views) {
    await page.goto(`/?e2e=disclosure#${route}`);
    const disclosure = page.locator(`${view} .page-context-disclosure`);
    await expect(disclosure).toHaveCount(1);
    await expect(disclosure).not.toHaveAttribute('open', '');
    await expect(disclosure.locator('p')).toBeHidden();
    await disclosure.locator('summary').click();
    await expect(disclosure.locator('p')).toBeVisible();
    await disclosure.locator('summary').click();
  }

  await page.goto('/?e2e=disclosure-sync#sync');
  const process = page.locator('.sync-process-disclosure');
  await expect(process).not.toHaveAttribute('open', '');
  await expect(process.locator('.sync-steps')).toBeHidden();
  await process.locator('summary').click();
  await expect(process.locator('.sync-steps')).toBeVisible();

  const workDisclosure = page.locator('#devices-view .page-context-disclosure');
  const locale = page.locator('#locale');
  for (const [value, label] of [
    ['zh-CN', '工作方式'],
    ['zh-TW', '運作方式'],
    ['ja', '仕組み'],
    ['en', 'How it works'],
  ]) {
    await locale.selectOption(value);
    await expect(workDisclosure.locator('summary')).toHaveText(label);
  }

  await locale.selectOption('zh-CN');
  await page.goto('/?e2e=disclosure-actions#sources');
  await expect(page.locator('#sources-view .safety-note')).toBeVisible();
  await expect(page.locator('#sources-view .safety-note')).toContainText('不修改源文件');
  await page.goto('/?e2e=disclosure-actions#packages');
  await expect(page.locator('#packages-view .action-footnote')).toBeVisible();
  await expect(page.locator('#packages-view .action-footnote')).toContainText('不删除额外文件');

  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/?e2e=disclosure-mobile#sync');
  await page.locator('#devices-view .page-context-disclosure summary').click();
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
});

test('game merge is previewed, rejects drift atomically, and fits handheld screens', async ({ page, request }) => {
  const createGame = async title => {
    const response = await request.post('/api/v1/games', { data: { default_title: title, platform: 'gba', titles: {} } });
    expect(response.ok()).toBeTruthy();
    return response.json();
  };
  const createEdition = async (gameID, title, editionType) => {
    const response = await request.post('/api/v1/editions', { data: { game_id: gameID, default_title: title, edition_type: editionType, languages: [], titles: {} } });
    expect(response.ok()).toBeTruthy();
    const game = await response.json();
    return game.editions.find(edition => edition.default_title === title);
  };

  const target = await createGame('Merge E2E Target');
  const source = await createGame('Merge E2E Source');
  const targetEdition = await createEdition(target.id, 'Original', 'original');
  const sourceEdition = await createEdition(source.id, 'Translation', 'translation');
  try {
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto('/?e2e=game-merge#library');
    await expect(page.locator(`.game-edit[data-game="${target.id}"]`)).toBeVisible();
    await page.locator(`.game-edit[data-game="${target.id}"]`).click();
    await page.locator('#merge-source').selectOption(source.id);
    await page.locator('#merge-game').click();
    await expect(page.locator('#merge-dialog')).toBeVisible();
    for (const [language, title] of [['zh-CN', '确认游戏合并'], ['zh-TW', '確認遊戲合併'], ['ja', 'ゲーム統合の確認'], ['en', 'Confirm game merge'], ['zh-CN', '确认游戏合并']]) {
      await page.locator('#locale').selectOption(language);
      await expect(page.locator('#merge-dialog h2')).toHaveText(title);
    }
    await expect(page.locator('#merge-preview-source')).toHaveText('Merge E2E Source');
    await expect(page.locator('#merge-preview-target')).toHaveText('Merge E2E Target');
    await expect(page.locator('#merge-preview-editions')).toHaveText('1 + 1 → 2');
    const geometry = await page.locator('#merge-dialog').evaluate(dialog => {
      const rect = dialog.getBoundingClientRect();
      return { left: rect.left, right: rect.right, overflow: dialog.scrollWidth - dialog.clientWidth, documentOverflow: document.documentElement.scrollWidth - document.documentElement.clientWidth };
    });
    expect(geometry.left).toBeGreaterThanOrEqual(0);
    expect(geometry.right).toBeLessThanOrEqual(390);
    expect(geometry.overflow).toBe(0);
    expect(geometry.documentOverflow).toBe(0);

    const driftEdition = await createEdition(source.id, 'Hard Mode', 'hack');
    await page.locator('#commit-game-merge').click();
    await expect(page.locator('#merge-dialog')).toBeHidden();
    await expect(page.locator('#notice')).toContainText('重新确认合并');
    page.clientErrors = page.clientErrors.filter(message => message !== 'console: Failed to load resource: the server responded with a status of 409 (Conflict)');
    expect((await request.get(`/api/v1/games/${target.id}`)).ok()).toBeTruthy();
    const sourceAfterStale = await (await request.get(`/api/v1/games/${source.id}`)).json();
    expect(sourceAfterStale.editions).toHaveLength(2);

    await expect(page.locator(`.game-edit[data-game="${target.id}"]`)).toBeVisible();
    await page.locator(`.game-edit[data-game="${target.id}"]`).click();
    await page.locator('#merge-source').selectOption(source.id);
    await page.locator('#merge-game').click();
    await expect(page.locator('#merge-preview-editions')).toHaveText('1 + 2 → 3');
    await page.locator('#commit-game-merge').click();
    await expect(page.locator('#notice')).toContainText('版本身份与存档关联保持不变');
    const merged = await (await request.get(`/api/v1/games/${target.id}`)).json();
    expect(merged.editions).toHaveLength(3);
    const identities = new Map(merged.editions.map(edition => [edition.id, edition.save_namespace]));
    expect(identities.get(targetEdition.id)).toBe(targetEdition.save_namespace);
    expect(identities.get(sourceEdition.id)).toBe(sourceEdition.save_namespace);
    expect(identities.get(driftEdition.id)).toBe(driftEdition.save_namespace);
    expect((await request.get(`/api/v1/games/${source.id}`)).status()).toBe(404);
  } finally {
    const targetResponse = await request.get(`/api/v1/games/${target.id}`);
    if (targetResponse.ok()) expect((await request.delete(`/api/v1/games/${target.id}`)).ok()).toBeTruthy();
    const sourceResponse = await request.get(`/api/v1/games/${source.id}`);
    if (sourceResponse.ok()) expect((await request.delete(`/api/v1/games/${source.id}`)).ok()).toBeTruthy();
  }
});

test('icons and primary workflow copy stay on the shared interface language', async ({ page }) => {
  const iconGameResponse = await page.request.post('/api/v1/games', { data: { default_title: 'Icon language fixture', platform: 'gba', titles: {} } });
  expect(iconGameResponse.ok()).toBe(true);
  const iconGame = await iconGameResponse.json();
  const iconEditionResponse = await page.request.post('/api/v1/editions', { data: { game_id: iconGame.id, default_title: 'Original edition', edition_type: 'original' } });
  expect(iconEditionResponse.ok()).toBe(true);

  await page.goto('/?e2e=ui-language#sources');
  await page.locator('input[name="import_kind"][value="metadata"]').locator('xpath=..').click();

  const sourceIconSlots = page.locator('.kind-icon, .source-logo, .policy-mark, .action-button b, .review-actions button b');
  expect(await sourceIconSlots.count()).toBeGreaterThan(0);
  expect(await sourceIconSlots.locator('svg use').count()).toBe(await sourceIconSlots.count());
  expect((await sourceIconSlots.allTextContents()).join('')).not.toMatch(/[＋✓→⌘]|CFG|META/);

  const symbolIDs = new Set(await page.locator('symbol').evaluateAll(symbols => symbols.map(symbol => symbol.id)));
  const references = await page.locator('svg use').evaluateAll(uses => uses.map(use => use.getAttribute('href')).filter(Boolean));
  for (const reference of references) expect(symbolIDs.has(reference.slice(1))).toBe(true);

  for (const selector of ['.import-source-card .flow-card-head>p', '.review-card .flow-card-head>p', '.missing-policy small', '#sources-view .action-footnote']) {
    const copy = (await page.locator(selector).innerText()).trim();
    expect(copy.length, `${selector} should stay concise`).toBeLessThanOrEqual(32);
  }

  await page.goto('/?e2e=ui-language-settings#settings');
  const settingsIcons = page.locator('.settings-icon');
  await expect(settingsIcons).toHaveCount(3);
  expect(await settingsIcons.locator('svg use').count()).toBe(3);
  expect((await settingsIcons.allTextContents()).join('')).toBe('');

  await page.goto('/?e2e=ui-language-packages#packages');
  await page.locator('.template-editor>summary').click();
  await expect(page.locator('#template-preset-list .template-preset-mark').first()).toBeVisible();
  const presetMarks = page.locator('#template-preset-list .template-preset-mark');
  expect(await presetMarks.locator('svg use[href="#icon-config"]').count()).toBe(await presetMarks.count());

  await page.goto('/?e2e=ui-language-library#library');
  await page.locator('[data-library-mode="covers"]').click();
  await expect(page.locator('.art-edit svg use[href="#icon-more"]').first()).toBeVisible();
  expect(await page.locator('.tile-add svg use[href="#icon-add"], .tile-empty-version svg use[href="#icon-add"]').count()).toBeGreaterThan(0);
  await page.locator('[data-library-mode="series"]').click();
  expect(await page.locator('.series-disclosure svg use[href="#icon-chevron"]').count()).toBeGreaterThan(0);
  await page.locator('[data-library-mode="list"]').click();
  await page.locator('.game-detail').first().click();
  await expect(page.locator('#game-detail-dialog [data-close] use[href="#icon-close"]')).toHaveCount(1);
  expect(await page.locator('#game-detail-dialog .detail-edition-row use[href="#icon-arrow"]').count()).toBeGreaterThan(0);
  await page.locator('#game-detail-dialog [data-close]').click();

  await page.goto('/?e2e=ui-language-template-remove#packages');
  await page.locator('.template-editor>summary').click();
  await page.locator('#add-package-template').click();
  await expect(page.locator('.template-remove use[href="#icon-close"]').last()).toBeVisible();

  const rawIconGlyphs = await page.locator('button, summary, .detail-edition-row>b, .sync-session-row small').evaluateAll(elements =>
    elements.map(element => element.textContent || '').filter(text => /[＋×⌄→•••↑↓✓]/.test(text))
  );
  expect(rawIconGlyphs).toEqual([]);
  expect((await page.request.delete(`/api/v1/games/${iconGame.id}`)).ok()).toBe(true);
});

test('English interface leaves no untranslated product copy in primary workspaces and editors', async ({ page }) => {
  const localeGameResponse = await page.request.post('/api/v1/games', { data: { default_title: 'Locale editor fixture', platform: 'gba', titles: {} } });
  expect(localeGameResponse.ok()).toBe(true);
  const localeGame = await localeGameResponse.json();
  const localeEditionResponse = await page.request.post('/api/v1/editions', { data: { game_id: localeGame.id, default_title: 'Locale edition fixture', edition_type: 'original' } });
  expect(localeEditionResponse.ok()).toBe(true);
  const auditVisibleProductCopy = async selector => page.locator(selector).evaluateAll(elements => {
    const visible = element => {
      const style = getComputedStyle(element);
      const rect = element.getBoundingClientRect();
      return style.display !== 'none' && style.visibility !== 'hidden' && Number(style.opacity) !== 0 && rect.width > 0 && rect.height > 0;
    };
    const findings = [];
    for (const root of elements) {
      for (const element of [root, ...root.querySelectorAll('*')]) {
        if (!visible(element) || element.closest('#locale, select[id$="-locale"], select[name$="locale"], code, pre, .management-copy, .candidate-copy, .history-meta, .runtime-item-copy')) continue;
        if (![...element.children].some(visible)) {
          const text = (element.textContent || '').trim();
          if (/[\u3400-\u9fff]/.test(text)) findings.push({ kind: 'text', text: text.slice(0, 80) });
        }
        for (const attribute of ['aria-label', 'placeholder', 'title']) {
          const value = element.getAttribute(attribute) || '';
          if (/[\u3400-\u9fff]/.test(value)) findings.push({ kind: attribute, text: value.slice(0, 80) });
        }
      }
    }
    return findings;
  });

  for (const route of ['platforms', 'sources', 'packages', 'sync', 'settings']) {
    await page.goto(`/?e2e=locale-contamination-${route}#${route}`);
    await page.locator('#locale').selectOption('en');
    await expect.poll(() => auditVisibleProductCopy('.topbar, .view:visible')).toEqual([]);
  }

  await page.goto('/?e2e=locale-library-content#library');
  await page.locator('#locale').selectOption('en');
  await expect.poll(() => auditVisibleProductCopy('.topbar, .management-head, .management-row')).toEqual([]);
  await page.locator('[data-library-mode="covers"]').click();
  await expect.poll(() => auditVisibleProductCopy('.topbar, .library-game-card')).toEqual([]);
  await page.locator('.library-game-card .art-open').click();
  await expect.poll(() => auditVisibleProductCopy('#game-detail-dialog')).toEqual([]);
  await page.locator('#game-detail-dialog [data-close]').click();

  const editorActions = [
    async () => { await page.goto('/?e2e=locale-game-editor#library'); await page.locator('#locale').selectOption('en'); await page.locator('#new-game').click(); },
    async () => { await page.goto('/?e2e=locale-series-editor#library'); await page.locator('#locale').selectOption('en'); await page.locator('[data-library-mode="series"]').click(); await expect.poll(() => auditVisibleProductCopy('.topbar, #series-grid')).toEqual([]); await page.locator('#new-game').click(); },
    async () => { await page.goto('/?e2e=locale-edition-editor#library'); await page.locator('#locale').selectOption('en'); await page.locator('.add-edition').first().click(); },
    async () => { await page.goto('/?e2e=locale-platform-editor#platforms'); await page.locator('#locale').selectOption('en'); await page.locator('#new-custom-platform').click(); },
    async () => { await page.goto('/?e2e=locale-runtime-editor#settings'); await page.locator('#locale').selectOption('en'); await page.locator('#new-runtime-item').click(); },
  ];
  for (const openEditor of editorActions) {
    await openEditor();
    await expect.poll(() => auditVisibleProductCopy('dialog[open]')).toEqual([]);
    await page.locator('dialog[open] [data-close]').first().click();
  }
  expect((await page.request.delete(`/api/v1/games/${localeGame.id}`)).ok()).toBe(true);
});

test('runtime-generated package controls localize immediately with grammatical counts', async ({ page }) => {
  await page.goto('/?e2e=dynamic-package-locales#packages');
  await page.setViewportSize({ width: 390, height: 844 });
  await page.locator('#locale').selectOption('en');
  await page.locator('.template-editor>summary').click();
  await expect(page.locator('#package-template-list .template-empty')).toHaveText('No custom templates. Frontend metadata will still be generated.');
  await expect(page.locator('#add-package-template')).toContainText('Start blank');

  await page.locator('#add-package-template').click();
  const template = page.locator('#package-template-list .package-template').first();
  const scope = template.locator('[data-template-field="scope"]');
  await expect(page.locator('#template-count')).toHaveText('1 template');
  await expect(template.locator('[data-template-field="name"]')).toHaveAttribute('placeholder', 'Template name');
  await expect(template.locator('[data-template-field="name"]')).toHaveAttribute('aria-label', 'Template name');
  await expect(scope).toHaveAttribute('aria-label', 'Template scope');
  await expect(scope.locator('option')).toHaveText(['Once per package', 'Per platform', 'Per game edition']);
  await expect(template.locator('.template-remove')).toHaveAttribute('aria-label', 'Remove template');
  await expect(template.locator('[data-template-field="output_path"]')).toHaveAttribute('aria-label', 'Relative output path');

  for (const [locale, count, name, scopeLabel, options, remove] of [
    ['zh-CN', '1 个模板', '模板名称', '模板范围', ['整合包一次', '每个平台', '每个游戏版本'], '移除模板'],
    ['zh-TW', '1 個範本', '範本名稱', '範本範圍', ['每個整合包一次', '每個平台', '每個遊戲版本'], '移除範本'],
    ['ja', '1 件のテンプレート', 'テンプレート名', 'テンプレート範囲', ['パッケージごと', '機種ごと', 'ゲームエディションごと'], 'テンプレートを削除'],
    ['en', '1 template', 'Template name', 'Template scope', ['Once per package', 'Per platform', 'Per game edition'], 'Remove template'],
  ]) {
    await page.locator('#locale').selectOption(locale);
    await expect(page.locator('#template-count')).toHaveText(count);
    await expect(template.locator('[data-template-field="name"]')).toHaveAttribute('placeholder', name);
    await expect(scope).toHaveAttribute('aria-label', scopeLabel);
    await expect(scope.locator('option')).toHaveText(options);
    await expect(template.locator('.template-remove')).toHaveAttribute('aria-label', remove);
  }

  await page.locator('#add-package-template').click();
  await expect(page.locator('#template-count')).toHaveText('2 templates');
  expect(await page.evaluate(() => [
    '1 个来源', '2 个来源', '1 项', '2 项', '将导入 1 项', '将导入 2 项',
    '1 个文件 · 1 个指纹', '2 个文件 · 1 个指纹', '成功导入 1 项，跳过 0 项；复制 1 个 ROM 文件、1 个媒体文件。',
  ].map(value => globalThis.uiI18n.t(value)))).toEqual([
    '1 source', '2 sources', '1 item', '2 items', 'Import 1 item', 'Import 2 items',
    '1 file · 1 hash', '2 files · 1 hash', 'Imported 1, skipped 0; copied 1 ROM file and 1 media file.',
  ]);
  expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBe(0);

  await page.locator('.template-remove').first().click();
  await page.locator('.template-remove').first().click();
  await expect(page.locator('#template-count')).toHaveText('0 templates');
  await expect(page.locator('#package-template-list .template-empty')).toHaveText('No custom templates. Frontend metadata will still be generated.');
});

test('typography stays readable without desktop or handheld overflow', async ({ page }) => {
  const routes = ['library', 'platforms', 'sources', 'packages', 'sync', 'settings'];
  const viewports = [
    { width: 1280, height: 720 },
    { width: 768, height: 1024 },
    { width: 390, height: 844 },
  ];

  for (const viewport of viewports) {
    await page.setViewportSize(viewport);
    for (const route of routes) {
      await page.goto(`/?e2e=readable-${viewport.width}-${route}#${route}`);
      await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
      const audit = await page.evaluate(() => {
        const undersized = [];
        const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
        while (walker.nextNode()) {
          const node = walker.currentNode;
          const text = node.textContent.trim();
          const element = node.parentElement;
          if (!text || !element || element.closest('script,style,svg,[hidden],[aria-hidden="true"],.action-footnote>span,#new-game,.nav-label')) continue;
          const style = getComputedStyle(element);
          const rect = element.getBoundingClientRect();
          if (style.display === 'none' || style.visibility === 'hidden' || Number(style.opacity) === 0 || rect.width === 0 || rect.height === 0) continue;
          const fontSize = Number.parseFloat(style.fontSize);
          if (fontSize < 11) undersized.push({ text: text.slice(0, 48), fontSize, className: element.className });
        }
        const controls = [...document.querySelectorAll('button:not(.icon-button):not(#new-game),input,select,textarea')]
          .filter(element => {
            const style = getComputedStyle(element);
            const rect = element.getBoundingClientRect();
            return style.display !== 'none' && style.visibility !== 'hidden' && rect.width > 0 && rect.height > 0;
          })
          .filter(element => Number.parseFloat(getComputedStyle(element).fontSize) < 14)
          .map(element => ({ text: (element.textContent || element.getAttribute('placeholder') || '').trim().slice(0, 48), fontSize: getComputedStyle(element).fontSize }));
        const touchTargets = window.innerWidth > 520 ? [] : [...document.querySelectorAll('button,select,summary,input:not([type="checkbox"]):not([type="radio"]):not([type="file"])')]
          .filter(element => {
            const style = getComputedStyle(element);
            const rect = element.getBoundingClientRect();
            return style.display !== 'none' && style.visibility !== 'hidden' && rect.width > 0 && rect.height > 0;
          })
          .filter(element => {
            const rect = element.getBoundingClientRect();
            return rect.width < 44 || rect.height < 44;
          })
          .map(element => {
            const rect = element.getBoundingClientRect();
            return { className: element.className, width: Math.round(rect.width), height: Math.round(rect.height) };
          });
        return {
          body: Number.parseFloat(getComputedStyle(document.body).fontSize),
          undersized,
          controls,
          touchTargets,
        };
      });
      expect(audit.body).toBeGreaterThanOrEqual(15);
      expect(audit.undersized, `${route} at ${viewport.width}px contains text below 11px`).toEqual([]);
      expect(audit.controls, `${route} at ${viewport.width}px contains controls below 14px`).toEqual([]);
      expect(audit.touchTargets, `${route} at ${viewport.width}px contains interactive targets below 44px`).toEqual([]);
    }
  }

  for (const viewport of [{ width: 768, height: 1024 }, { width: 390, height: 844 }]) {
    await page.setViewportSize(viewport);
    await page.goto(`/?e2e=readable-${viewport.width}-navigation#library`);
    for (const locale of ['zh-CN', 'zh-TW', 'ja', 'en']) {
      await page.locator('#locale').selectOption(locale);
      const navigationAudit = await page.locator('.sidebar .nav-label').evaluateAll(labels => {
        const context = document.createElement('canvas').getContext('2d');
        const compact = labels.map(label => {
          const style = getComputedStyle(label, '::after');
          const content = style.content.replace(/^['"]|['"]$/g, '');
          context.font = `${style.fontWeight} ${style.fontSize} ${style.fontFamily}`;
          return { content, fontSize: Number.parseFloat(style.fontSize), available: label.clientWidth, measured: context.measureText(content).width };
        });
        return { compact, clipped: compact.filter(label => label.measured > label.available + 1).map(label => label.content) };
      });
      expect(navigationAudit.clipped, `${locale} navigation clips labels at ${viewport.width}px`).toEqual([]);
      expect(navigationAudit.compact.every(label => label.content !== 'none' && label.fontSize >= 11)).toBe(true);
    }
  }
});

test('web client loads every collection page and rejects pagination drift', async ({ page }) => {
  const games = Array.from({ length: 205 }, (_, index) => ({
    id: `paged-game-${String(index).padStart(3, '0')}`,
    default_title: `Paged Game ${index + 1}`,
    display_title: `Paged Game ${index + 1}`,
    platform: 'gba',
    titles: {},
    editions: [],
    media: [],
    created_at: '2026-08-27T00:00:00Z',
    updated_at: '2026-08-27T00:00:00Z',
  }));
  const pageRequests = [];
  await page.route('**/api/v1/games?*', async route => {
    const url = new URL(route.request().url());
    const limit = Number(url.searchParams.get('limit'));
    const offset = Number(url.searchParams.get('offset'));
    pageRequests.push({ limit, offset });
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ data: games.slice(offset, offset + limit), pagination: { limit, offset, total: games.length } }),
    });
  });
  await page.goto('/?e2e=all-pages#library');
  await expect(page.locator('.management-row')).toHaveCount(205);
  await expect(page.locator('#result-count')).toHaveText('205 / 205');
  expect(pageRequests).toEqual([{ limit: 200, offset: 0 }, { limit: 200, offset: 200 }]);

  await page.unroute('**/api/v1/games?*');
  await page.route('**/api/v1/games?*', async route => {
    const url = new URL(route.request().url());
    const limit = Number(url.searchParams.get('limit'));
    const offset = Number(url.searchParams.get('offset'));
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ data: games.slice(offset, offset + limit), pagination: { limit, offset, total: offset === 0 ? games.length : games.length + 1 } }),
    });
  });
  await page.goto('/?e2e=page-drift#library');
  await expect(page.locator('#notice.error')).toContainText('资料库已更新，请重试。');
  await expect(page.locator('.management-row')).toHaveCount(0);
});

test('library requests summary projections and loads private game detail on demand', async ({ page }) => {
  const summary = {
    id: 'lazy-game', default_title: 'Lazy Hero', display_title: 'Lazy Hero', platform: 'gba', titles: {},
    editions: [{
      id: 'lazy-edition', game_id: 'lazy-game', default_title: 'Original', display_title: 'Original',
      edition_type: 'original', languages: ['en'], save_namespace: 'lazy-save-namespace', sort_order: 0,
      titles: {}, artifact_stats: { total: 1, missing: 0, hashed: 1, usable: 1, managed: 0 },
    }],
    created_at: '2026-08-31T00:00:00Z', updated_at: '2026-08-31T00:00:00Z',
  };
  const detail = {
    ...summary,
    editions: [{
      ...summary.editions[0],
      artifacts: [{ id: 'lazy-artifact', edition_id: 'lazy-edition', path: 'private/lazy.gba', storage_kind: 'library', role: 'rom', size: 1024, sha256: 'a'.repeat(64), missing: false }],
      media: [],
    }],
    media: [],
  };
  const listProjections = [];
  let detailRequests = 0;
  await page.route(/\/api\/v1\/games\?.*$/, async route => {
    const url = new URL(route.request().url()), limit = Number(url.searchParams.get('limit')), offset = Number(url.searchParams.get('offset'));
    listProjections.push(url.searchParams.get('projection'));
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ data: offset ? [] : [summary], pagination: { limit, offset, total: 1 } }) });
  });
  await page.route(/\/api\/v1\/series\?.*$/, async route => {
    const url = new URL(route.request().url()), limit = Number(url.searchParams.get('limit')), offset = Number(url.searchParams.get('offset'));
    expect(url.searchParams.get('projection')).toBe('summary');
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ data: [], pagination: { limit, offset, total: 0 } }) });
  });
  await page.route(/\/api\/v1\/games\/lazy-game\?.*$/, async route => {
    detailRequests += 1;
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(detail) });
  });

  await page.goto('/?e2e=summary-detail#library');
  await expect(page.locator('.management-row')).toHaveCount(1);
  expect(listProjections).toEqual(['summary']);
  expect(detailRequests).toBe(0);
  await page.locator('.management-row .management-copy .game-detail').click();
  await expect(page.locator('#game-detail-dialog')).toBeVisible();
  await expect(page.locator('#game-detail-dialog')).toContainText('1');
  expect(detailRequests).toBe(1);
});

test('custom platform editor persists scan and frontend mappings without raw slug entry', async ({ page }) => {
  await page.goto('/?e2e=custom-platform#platforms');
  await page.locator('#new-custom-platform').click();
  const dialog = page.locator('#platform-editor-dialog');
  await expect(dialog).toBeVisible();
  await dialog.locator('[name="id"]').fill('fixture-handheld');
  await dialog.locator('[name="name"]').fill('Fixture Handheld');
  await dialog.locator('[name="name_zh"]').fill('测试掌机');
  await dialog.locator('[name="vendor"]').fill('Community');
  await dialog.locator('[name="aliases"]').fill('fixture-hh');
  await dialog.locator('[name="extensions"]').fill('.opk, directory');
  await dialog.locator('[name="esde_systems"]').fill('fixture-handheld-es');
  await dialog.locator('[name="emulators_handheld_linux"]').fill('OpenEmu Runner');
  await dialog.locator('button[type="submit"]').click();
  await expect(dialog).toBeHidden();
  const row = page.locator('.platform-row', { has: page.locator('code', { hasText: 'fixture-handheld' }) });
  await expect(row).toBeVisible();
  await expect(row).toContainText('.opk');
  await expect(row).toContainText('fixture-handheld-es');
  const locale = page.locator('#locale');
  for (const [value, heading, action] of [
    ['zh-TW', '平台名稱', '編輯平台'],
    ['ja', '機種名', '機種を編集'],
    ['en', 'Platform', 'Edit platform'],
    ['zh-CN', '平台名称', '编辑平台'],
  ]) {
    await locale.selectOption(value);
    await expect(page.locator('.platform-directory-head')).toContainText(heading);
    await expect(row).toContainText(action);
  }

  await page.locator('a[href="#sources"]').click();
  await expect(page.locator('#import-platform-preset option[value="fixture-handheld"]')).toHaveText(/测试掌机 · fixture-handheld/);
  await page.locator('a[href="#platforms"]').click();
  await row.locator('[data-edit-platform="fixture-handheld"]').click();
  await expect(dialog.locator('[name="id"]')).toBeDisabled();
  await dialog.locator('[name="enabled"]').uncheck();
  await dialog.locator('button[type="submit"]').click();
  await expect(row).toContainText('已停用');
  await expect(page.locator('#import-platform-preset option[value="fixture-handheld"]')).toHaveCount(0);

  await row.locator('[data-edit-platform="fixture-handheld"]').click();
  page.once('dialog', prompt => prompt.accept());
  await dialog.locator('#delete-custom-platform').click();
  await expect(row).toHaveCount(0);
  await locale.selectOption('en');
  await page.locator('#new-custom-platform').click();
  await expect(dialog.locator('#platform-editor-title')).toHaveText('Create custom platform');
  await expect(dialog.locator('[name="name_zh"]')).toHaveAttribute('placeholder', 'Custom handheld');
  await expect(dialog).not.toContainText(/[\u3400-\u9fff]/);
  await dialog.locator('[data-close]').first().click();
});

test('signed direct-ROM preview commits the selected batch end to end', async ({ page }) => {
  await openTransfer(page);
  await choosePlatform(page);
  await page.locator('input[name="rom_source"]').fill('pegasus/gba');
  await page.locator('#preview-import').click();

  await expect(page.locator('#import-review')).toBeVisible();
  await expect(page.locator('#import-preview .preview-item')).toHaveCount(3);
  await expect(page.locator('#import-preview .status-pill.new')).toHaveCount(3);
  await expect(page.locator('#import-preview input:checked')).toHaveCount(3);
  await expect(page.locator('#selection-count')).toContainText('3');
  await expect(page.locator('#commit-import')).toBeVisible();

  const tokens = await page.locator('#import-preview input').evaluateAll(inputs => inputs.map(input => input.value));
  expect(tokens).toHaveLength(3);
  expect(new Set(tokens).size).toBe(3);
  expect(tokens.every(token => token.length >= 32)).toBe(true);

  await page.locator('#commit-import').click();
  await expect(page.locator('#import-inline-state')).toContainText('成功导入 3 项');
  await page.locator('#locale').selectOption('en');
  await expect(page.locator('#import-inline-state')).toContainText('Imported 3, skipped 0');
  await expect(page.locator('#commit-summary')).toHaveText('Import complete');
  await expect(page.locator('#import-inline-state')).not.toContainText(/[\u3400-\u9fff]/);
  await page.locator('#locale').selectOption('zh-CN');
  const gamesResponse = await page.request.get('/api/v1/games');
  expect(gamesResponse.ok()).toBe(true);
  const games = await gamesResponse.json();
  expect(games.data).toHaveLength(3);
  expect(games.data[0].editions[0]).toHaveProperty('game_id');
  expect(games.data[0].editions[0]).not.toHaveProperty('work_id');

  const emptyGameResponse = await page.request.post('/api/v1/games', {
    data: { default_title: 'Metadata only', platform: 'nds', titles: {} }
  });
  expect(emptyGameResponse.ok()).toBe(true);

  await page.goto('/?e2e=management#library');
  await expect(page.locator('body')).not.toContainText('作品');
  await expect(page.locator('body')).not.toContainText(/\bWork\b/);
  await expect(page.locator('[data-library-mode="list"]')).toHaveClass(/active/);
  await expect(page.locator('#games.management-table .management-row')).toHaveCount(4);
  await expect(page.locator('.management-head')).toContainText('ROM');
  await expect(page.locator('.management-files .health-state.ready')).toHaveCount(3);
  await expect(page.locator('.management-files .health-state.unlinked')).toHaveCount(1);
  const rowHeights = await page.locator('.management-row').evaluateAll(rows => rows.map(row => Math.round(row.getBoundingClientRect().height)));
  expect(new Set(rowHeights).size).toBe(1);

  await page.locator('[data-library-mode="covers"]').click();
  await expect(page.locator('#games.cover-grid .library-game-card')).toHaveCount(4);
  await page.locator('[data-library-mode="series"]').click();
  await expect(page.locator('#series-grid .ungrouped-group .series-child-row')).toHaveCount(4);
  await expect(page.locator('.ungrouped-group .series-child-row').first()).toBeVisible();
  await page.locator('.ungrouped-group summary').click();
  await expect(page.locator('.ungrouped-group .series-child-row').first()).toBeHidden();
  await page.locator('.ungrouped-group summary').click();
  await expect(page.locator('.ungrouped-group .series-child-row').first()).toBeVisible();
  await page.locator('[data-library-mode="list"]').click();
  await expect(page.locator('#games.management-table')).toBeVisible();

  const targetGame = games.data[0];
  const mediaFile = {
    name: 'shared-cover.png',
    mimeType: 'image/png',
    buffer: Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=', 'base64')
  };
  const targetRow = page.locator('.management-row', { hasText: targetGame.display_title });
  await targetRow.locator('.game-edit').click();
  await expect(page.locator('#game-media-panel')).toBeVisible();
  await page.locator('#game-media-file').setInputFiles(mediaFile);
  const [thumbnailResponse] = await Promise.all([
    page.waitForResponse(response => response.url().includes('/thumbnail?size=128') && response.request().method() === 'GET'),
    page.locator('#upload-game-media').click()
  ]);
  expect(thumbnailResponse.ok()).toBe(true);
  expect(thumbnailResponse.headers()['content-type']).toBe('image/png');
  expect(thumbnailResponse.headers().etag).toContain('thumbnail-v1:');
  await expect(page.locator('#game-media .media-row')).toHaveCount(1);
  const gameMediaResponse = await page.request.get(`/api/v1/media?game_id=${targetGame.id}`);
  expect(gameMediaResponse.ok()).toBe(true);
  const gameMedia = (await gameMediaResponse.json()).data;
  expect(gameMedia).toHaveLength(1);
  expect(gameMedia[0].game_id).toBe(targetGame.id);
  expect(gameMedia[0]).not.toHaveProperty('edition_id');
  expect(gameMedia[0].content_status).toBe('available');
  expect(gameMedia[0].content_checked_at).not.toBe('');
  await expect(page.locator('#game-media .media-row').first()).toContainText('文件可用');
  const [mediaRecheckResponse] = await Promise.all([
    page.waitForResponse(response => response.url().includes('/api/v1/media/recheck?game_id=') && response.request().method() === 'POST'),
    page.locator('#game-media-panel .recheck-media').click()
  ]);
  expect(mediaRecheckResponse.ok()).toBe(true);
  const mediaRecheck = await mediaRecheckResponse.json();
  expect(mediaRecheck.checked).toBe(1);
  expect(mediaRecheck.available).toBe(1);
  await expect(page.locator('#game-media .media-row').first()).toContainText('文件可用');
  const gameMediaRow = page.locator('#game-media .media-row').first();
  await gameMediaRow.locator('.edit-media-meta').click();
  await expect(gameMediaRow.locator('.media-edit-panel small')).toHaveText('仅修改分类信息，不移动文件或更改归属。');
  await expect(gameMediaRow.locator('.media-edit-panel small')).not.toHaveAttribute('data-tooltip', /.+/);
  await gameMediaRow.locator('.media-edit-kind').selectOption('poster');
  await gameMediaRow.locator('.media-edit-locale').selectOption('en');
  await gameMediaRow.locator('.media-edit-order').fill('3');
  await gameMediaRow.locator('.save-media-edit').click();
  await expect(page.locator('#game-media .media-row').first()).toContainText('海报');
  const editedGameMedia = await (await page.request.get(`/api/v1/media/${gameMedia[0].id}`)).json();
  expect(editedGameMedia.kind).toBe('poster');
  expect(editedGameMedia.locale).toBe('en');
  expect(editedGameMedia.sort_order).toBe(3);
  expect(editedGameMedia.path).toBe(gameMedia[0].path);
  expect(editedGameMedia.sha256).toBe(gameMedia[0].sha256);
  expect(editedGameMedia.game_id).toBe(gameMedia[0].game_id);
  await page.locator('#game-media .media-row').first().locator('.edit-media-meta').click();
  await page.locator('#game-media .media-row').first().locator('.media-edit-kind').selectOption('cover');
  await page.locator('#game-media .media-row').first().locator('.media-edit-locale').selectOption('');
  await page.locator('#game-media .media-row').first().locator('.media-edit-order').fill('0');
  await page.locator('#game-media .media-row').first().locator('.save-media-edit').click();
  await expect(page.locator('#game-media .media-row').first()).toContainText('封面');
  await page.locator('#game-form [data-close]').first().click();

  await targetRow.locator('.game-detail').first().click();
  await page.locator(`.detail-edition-row[data-edition="${targetGame.editions[0].id}"]`).click();
  await expect(page.locator('#edition-dialog')).toBeVisible();
  const artifactRow = page.locator('.artifact-row').first();
  await artifactRow.locator('.edit-artifact').click();
  await expect(artifactRow.locator('.artifact-edit-panel small')).toHaveText('仅修改分类与碟号，不改 ROM 文件。');
  await expect(artifactRow.locator('.artifact-edit-panel small')).not.toHaveAttribute('data-tooltip', /.+/);
  await artifactRow.locator('.artifact-edit-role').selectOption('disc');
  await artifactRow.locator('.artifact-edit-disc').fill('2');
  await artifactRow.locator('.save-artifact-edit').click();
  await expect(page.locator('.artifact-row').first().locator('.artifact-kind')).toHaveText('光盘 2');
  const editedArtifact = await (await page.request.get(`/api/v1/artifacts/${targetGame.editions[0].artifacts[0].id}`)).json();
  expect(editedArtifact.role).toBe('disc');
  expect(editedArtifact.disc_index).toBe(2);
  await page.locator('.artifact-row').first().locator('.edit-artifact').click();
  await page.locator('.artifact-row').first().locator('.artifact-edit-role').selectOption('rom');
  await page.locator('.artifact-row').first().locator('.artifact-edit-disc').fill('0');
  await page.locator('.artifact-row').first().locator('.save-artifact-edit').click();
  await expect(page.locator('.artifact-row').first().locator('.artifact-kind')).toHaveText('ROM');
  await page.locator('#media-file').setInputFiles(mediaFile);
  await page.locator('#upload-media').click();
  await expect(page.locator('#edition-media .media-row')).toHaveCount(1);
  const editionMediaResponse = await page.request.get(`/api/v1/media?edition_id=${targetGame.editions[0].id}`);
  expect(editionMediaResponse.ok()).toBe(true);
  const editionMedia = (await editionMediaResponse.json()).data;
  expect(editionMedia).toHaveLength(1);
  expect(editionMedia[0].edition_id).toBe(targetGame.editions[0].id);
  expect(editionMedia[0].sha256).toBe(gameMedia[0].sha256);
  expect(editionMedia[0].path).toBe(gameMedia[0].path);
  expect(editionMedia[0].content_status).toBe('available');
  await expect(page.locator('#edition-media .media-row').first()).toContainText('文件可用');
});

test('metadata import can resolve ROMs from a separate explicit content folder', async ({ page }) => {
  await openTransfer(page);
  await page.locator('input[name="import_kind"][value="metadata"]').locator('xpath=..').click();
  await choosePlatform(page);
  await page.locator('input[name="source"]').fill('pegasus/gba/metadata.pegasus.txt');
  await expect(page.locator('#content-root-field')).toBeVisible();
  await page.locator('input[name="content_root"]').fill('pegasus/gba');
  await page.locator('input[name="remember_source"]').uncheck();
  await page.locator('select[name="media_storage"]').selectOption('ignore');
  await page.locator('#preview-import').click();

  await expect(page.locator('#import-review')).toBeVisible();
  await expect(page.locator('#import-preview .preview-item')).toHaveCount(2);
  await expect(page.locator('#import-preview .status-pill.missing')).toHaveCount(0);
  await expect(page.locator('#import-preview .preview-item').first()).toContainText('pegasus/gba/');

  await page.locator('input[name="format"][value="varkiv"]').locator('xpath=..').click();
  await expect(page.locator('#content-root-field')).toBeHidden();
  await expect(page.locator('input[name="content_root"]')).toBeDisabled();
});

test('series metadata and cross-platform membership save as one atomic graph', async ({ page }) => {
  const firstGameResponse = await page.request.post('/api/v1/games', { data: { default_title: 'Series Fixture GBA', platform: 'gba', titles: {} } });
  const secondGameResponse = await page.request.post('/api/v1/games', { data: { default_title: 'Series Fixture NDS', platform: 'nds', titles: {} } });
  expect(firstGameResponse.ok()).toBe(true);
  expect(secondGameResponse.ok()).toBe(true);
  const firstID = (await firstGameResponse.json()).id;
  const secondID = (await secondGameResponse.json()).id;
  const mutations = [];
  page.on('request', request => {
    const pathname = new URL(request.url()).pathname;
    if (['POST', 'PUT'].includes(request.method()) && /^\/api\/v1\/series(?:\/[^/]+)?$/.test(pathname)) {
      mutations.push({ method: request.method(), pathname, body: request.postDataJSON() });
    }
  });

  await page.goto('/?e2e=series-graph#library');
  await page.locator('[data-library-mode="series"]').click();
  await page.locator('#new-game').click();
  const dialog = page.locator('#series-dialog');
  await expect(dialog).toBeVisible();
  await dialog.locator('[name="default_title"]').fill('Portable Strategy Saga');
  await dialog.locator('[name="description"]').fill('Cross-platform fixture');
  const rows = dialog.locator('[data-series-member]');
  await expect(rows).not.toHaveCount(0);
  const firstCreated = rows.filter({ has: page.locator(`input[value="${firstID}"]`) });
  const secondCreated = rows.filter({ has: page.locator(`input[value="${secondID}"]`) });
  await firstCreated.locator('input[type="checkbox"]').check();
  await firstCreated.locator('select').selectOption('mainline');
  await firstCreated.locator('.member-order').fill('10');
  await secondCreated.locator('input[type="checkbox"]').check();
  await secondCreated.locator('select').selectOption('port');
  await secondCreated.locator('.member-order').fill('20');
  await dialog.locator('button[type="submit"]').click();
  await expect(dialog).toBeHidden();
  await expect(page.locator('.series-group', { hasText: 'Portable Strategy Saga' })).toHaveCount(1);
  expect(mutations).toHaveLength(1);
  expect(mutations[0].method).toBe('POST');
  expect(mutations[0].body.members).toEqual([
    { game_id: firstID, relation_type: 'mainline', sort_order: 10 },
    { game_id: secondID, relation_type: 'port', sort_order: 20 },
  ]);

  const list = await (await page.request.get('/api/v1/series?q=Portable%20Strategy%20Saga')).json();
  expect(list.data).toHaveLength(1);
  const seriesID = list.data[0].id;
  const group = page.locator('.series-group', { hasText: 'Portable Strategy Saga' });
  await group.locator('.series-edit').click();
  await dialog.locator('[name="default_title"]').fill('Portable Strategy Saga II');
  const first = dialog.locator(`[data-series-member="${firstID}"]`);
  const second = dialog.locator(`[data-series-member="${secondID}"]`);
  await first.locator('input[type="checkbox"]').uncheck();
  await second.locator('select').selectOption('remake');
  await second.locator('.member-order').fill('3');
  await dialog.locator('button[type="submit"]').click();
  await expect(dialog).toBeHidden();
  expect(mutations).toHaveLength(2);
  expect(mutations[1].method).toBe('PUT');
  expect(mutations[1].body.members).toEqual([{ game_id: secondID, relation_type: 'remake', sort_order: 3 }]);

  const updated = await (await page.request.get(`/api/v1/series/${seriesID}`)).json();
  expect(updated.default_title).toBe('Portable Strategy Saga II');
  expect(updated.members).toHaveLength(1);
  expect(updated.members[0]).toMatchObject({ game_id: secondID, relation_type: 'remake', sort_order: 3 });
  expect(mutations.some(request => request.pathname.includes('/members/'))).toBe(false);
  expect((await page.request.get(`/api/v1/games/${firstID}`)).ok()).toBe(true);
  expect((await page.request.delete(`/api/v1/series/${seriesID}`)).ok()).toBe(true);
  expect((await page.request.delete(`/api/v1/games/${firstID}`)).ok()).toBe(true);
  expect((await page.request.delete(`/api/v1/games/${secondID}`)).ok()).toBe(true);
});

test('saved source keeps scan history and can be disabled without touching files', async ({ page }) => {
  await openTransfer(page);
  await expect(page.locator('#source-registry-list .saved-source')).toHaveCount(1);
  const source = page.locator('#source-registry-list .saved-source').first();
  await expect(source).toContainText('最近导入成功');
  await expect(source.locator('.saved-source-copy')).toContainText('pegasus/gba');
  await page.locator('#locale').selectOption('en');
  await expect(source.locator('.saved-source-copy')).toContainText('Last import succeeded');
  await expect(source.locator('.saved-source-copy')).not.toContainText(/[\u3400-\u9fff]/);
  await page.locator('#locale').selectOption('zh-CN');

  const sourcesResponse = await page.request.get('/api/v1/sources');
  expect(sourcesResponse.ok()).toBe(true);
  const sources = await sourcesResponse.json();
  expect(sources.data).toHaveLength(1);
  const scansResponse = await page.request.get(`/api/v1/source-scans?source_id=${sources.data[0].id}`);
  expect(scansResponse.ok()).toBe(true);
  const scans = await scansResponse.json();
  expect(scans.data).toHaveLength(1);
  expect(scans.data[0].status).toBe('committed');

  await source.locator('.source-toggle').click();
  await expect(page.locator('#source-registry-list .saved-source')).toHaveClass(/disabled/);
  await expect(page.locator('#source-registry-list .source-scan')).toBeDisabled();
  await page.locator('#source-registry-list .source-toggle').click();
  await expect(page.locator('#source-registry-list .saved-source')).not.toHaveClass(/disabled/);
});

test('package workflow persists a profile, previews writes, renders safe config, then records a release', async ({ page }) => {
  await page.goto('/?e2e=package-plan#packages');
  await expect(page.locator('#packages-view')).toBeVisible();
  await expect(page.locator('.profile-icon svg circle, .profile-icon svg [rx]')).toHaveCount(0);
  expect(await page.locator('.profile-icon').first().evaluate(element => getComputedStyle(element).clipPath)).toContain('polygon');
  expect(await page.locator('.profile-icon svg').first().evaluate(element => getComputedStyle(element).strokeLinecap)).toBe('square');
  await page.locator('.template-editor>summary').click();
  const presetResponse = await page.request.get('/api/v1/config-template-presets?target=android&frontend=pegasus');
  expect(presetResponse.ok()).toBe(true);
  const presetCatalog = await presetResponse.json();
  expect(presetCatalog.data).toHaveLength(4);
  const launchStarterPreset = presetCatalog.data.find(item => item.id === 'builtin-template-launch-resolution');
  expect(launchStarterPreset.contract_version).toBe(2);
  expect(launchStarterPreset.body).toContain('{{launch.arguments_json}}');
  expect(launchStarterPreset.body).toContain('{{launch.executable_hints_json}}');
  await expect(page.locator('#template-preset-list .template-preset-row')).toHaveCount(4);
  await expect(page.locator('[data-template-preset="builtin-template-launch-resolution"]')).toContainText('解析后的参数数组');
  const starter = page.locator('[data-template-preset="builtin-template-device-directories"]');
  await expect(starter.locator('.template-preset-mark svg use')).toHaveAttribute('href', '#icon-config');
  await starter.locator('button').click();
  await expect(starter.locator('button')).toBeDisabled();
  await expect(page.locator('#package-template-list .package-template')).toHaveCount(1);
  await page.locator('[data-template-field="name"]').fill('RetroArch options');
  await page.locator('[data-template-field="scope"]').selectOption('edition');
  await page.locator('[data-template-field="output_path"]').fill('config/{{platform.id}}/{{edition.id}}.cfg');
  await page.locator('[data-template-field="body"]').fill('rom={{rom.path}}\nvideo_fullscreen=true\n');
  await page.locator('#build-package').click();

  await expect(page.locator('#package-result')).toBeVisible();
  await expect(page.locator('#package-result')).toContainText('可以安全构建');
  await expect(page.locator('#confirm-package-build')).toBeVisible();
  const plansResponse = await page.request.get('/api/v1/package-plans');
  expect(plansResponse.ok()).toBe(true);
  const plans = await plansResponse.json();
  expect(plans.data).toHaveLength(1);
  expect(plans.data[0].status).toBe('ready');
  expect(plans.data[0].plan.items.some(item => item.kind === 'config')).toBe(true);
  expect(plans.data[0].plan.space_checked).toBe(true);
  expect(plans.data[0].plan.estimated_write_bytes).toBeGreaterThan(0);
  expect(plans.data[0].plan.available_bytes).toBeGreaterThan(plans.data[0].plan.estimated_write_bytes);
  await expect(page.locator('.plan-space')).toContainText('预计写入空间');
  await expect(page.locator('.plan-space')).toContainText('目标可用空间');

  await page.locator('#confirm-package-build').click();
  await expect(page.locator('#package-result')).toContainText('整合包已经就绪');
  await expect(page.locator('#confirm-package-build')).toBeHidden();
  const releasesResponse = await page.request.get('/api/v1/package-releases');
  expect(releasesResponse.ok()).toBe(true);
  const releases = await releasesResponse.json();
  expect(releases.data).toHaveLength(1);
  expect(releases.data[0].status).toBe('succeeded');
	const recoveryPrefix = `state/recovery/packages/${releases.data[0].output_slug}/release-`;

  await page.locator('#build-package').click();
  await expect(page.locator('#confirm-package-build')).toBeVisible();
  await page.locator('#confirm-package-build').click();
  await expect(page.locator('.package-recovery')).toBeVisible();
  await expect(page.locator('.package-recovery code')).toHaveText(new RegExp(`^${recoveryPrefix}[^/]+$`));
  await expect(page.locator('.package-recovery')).toContainText('更新前快照');
  const updatedReleasesResponse = await page.request.get('/api/v1/package-releases');
  const updatedReleases = await updatedReleasesResponse.json();
  expect(updatedReleases.data).toHaveLength(2);
  expect(updatedReleases.data[0].result.recovery_snapshot).toMatch(new RegExp(`^${recoveryPrefix}[^/]+$`));

  await page.locator('#locale').selectOption('en');
  await expect(page.locator('.template-editor>summary')).toContainText('Configuration templates');
  await expect(page.locator('#template-catalog-title')).toHaveText('Built-in starters');
  await expect(page.locator('#template-preset-list')).not.toContainText(/[㐀-鿿]/);
  await expect(page.locator('#build-package span')).toHaveText('Regenerate build plan');
  await page.locator('#build-package').click();
  await expect(page.locator('.plan-space')).toContainText('Estimated write size');
  await expect(page.locator('.plan-space')).toContainText('Destination free space');
  await expect(page.locator('#package-result')).not.toContainText(/需要留意|预计写入空间|目标可用空间/);
  await page.locator('#package-mode').selectOption('reference');
  await expect(page.locator('#package-mode option:checked')).toHaveText('Metadata only');
  await expect(page.locator('#summary-mode')).toHaveText('Relative-path metadata');
  await expect(page.locator('#reference-mode-note')).toBeVisible();
  await expect(page.locator('#reference-mode-note')).toContainText('Metadata only; content is not copied');
  await expect(page.locator('#reference-mode-note')).not.toContainText(/[㐀-鿿]/);
  for (const [locale, heading] of [
    ['zh-CN', '只生成元数据，不复制内容'],
    ['zh-TW', '只產生中繼資料，不複製內容'],
    ['ja', 'メタデータだけを生成し、コンテンツはコピーしません'],
    ['en', 'Metadata only; content is not copied'],
  ]) {
    await page.locator('#locale').selectOption(locale);
    await expect(page.locator('#reference-mode-note strong')).toHaveText(heading);
  }
  const navIconStyle = await page.locator('.nav-item svg').evaluateAll(icons => icons.map(icon => ({
    linecap: getComputedStyle(icon).strokeLinecap,
    join: getComputedStyle(icon).strokeLinejoin,
    href: icon.querySelector('use')?.getAttribute('href') || '',
  })));
  expect(navIconStyle).toHaveLength(6);
  expect(navIconStyle.every(icon => icon.linecap === 'square' && icon.join === 'miter' && icon.href.startsWith('#icon-'))).toBe(true);
  await page.setViewportSize({ width: 390, height: 844 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBe(0);
  expect(await page.locator('.template-preset-row button').evaluateAll(buttons => Math.min(...buttons.map(button => button.getBoundingClientRect().height)))).toBeGreaterThanOrEqual(38);

  const builtinProfilesBefore = await (await page.request.get('/api/v1/package-profiles')).json();
  const portableBuiltinBefore = builtinProfilesBefore.data.find(item => item.id === 'builtin-portable-pegasus-en');
  expect(portableBuiltinBefore.builtin).toBe(true);
  expect(portableBuiltinBefore.templates).toHaveLength(0);
  const copiedProfile = await page.evaluate(async () => window.ensurePackageProfile({
    name: 'portable-pegasus-en', frontend: 'pegasus', target: 'portable', locale: 'en', file_mode: 'copy',
    output_slug: 'portable-pegasus-en', enabled: true,
    templates: [{ name: 'Custom portable options', scope: 'edition', output_path: 'config/{{edition.id}}.cfg', body: 'rom={{rom.path}}\n' }],
  }));
  expect(copiedProfile.builtin).toBe(false);
  expect(copiedProfile.output_slug).toBe('portable-pegasus-en-custom');
  const portableBuiltinAfter = await (await page.request.get('/api/v1/package-profiles/builtin-portable-pegasus-en')).json();
  expect(portableBuiltinAfter.builtin).toBe(true);
  expect(portableBuiltinAfter.templates).toHaveLength(0);
});

test('runtime catalog binds an edition to RetroArch and exports a portable launch manifest', async ({ page }) => {
  await page.goto('/?e2e=runtime-catalog#settings');
  await expect(page.locator('#runtime-catalog article')).toHaveCount(5);
  await expect(page.locator('#runtime-total')).toHaveText('88');
  await expect(page.locator('#runtime-catalog article').nth(0).locator('li')).toHaveCount(4);
  await expect(page.locator('#runtime-catalog article').nth(1).locator('li')).toHaveCount(2);
  await expect(page.locator('#runtime-catalog article').nth(2).locator('li')).toHaveCount(10);
  await expect(page.locator('#runtime-catalog article').nth(3).locator('li')).toHaveCount(25);
  await expect(page.locator('#runtime-catalog article').nth(3)).toContainText('xemu');
  await expect(page.locator('#runtime-catalog article').nth(3)).toContainText('Xenia');
  await expect(page.locator('#runtime-catalog article').nth(3)).toContainText('BigPEmu');
  await expect(page.locator('#runtime-catalog article').nth(3)).toContainText('Tsugaru');
  await expect(page.locator('#runtime-catalog article').nth(4).locator('li')).toHaveCount(47);
  await expect(page.locator('#runtime-catalog article').nth(4)).toContainText('60 条映射');
  expect(await page.evaluate(() => ({
    serial: savePathIdentityError({ serial: '' }, ['{{device.save_dir}}/{{edition.serial}}/save.bin']),
    product: savePathIdentityError({ product_code: '' }, ['{{driver.user_dir}}/{{edition.product_code}}']),
    splitTitle: savePathIdentityError({ title_id: 'not-a-title-id' }, ['{{driver.user_dir}}/{{edition.title_id_high}}{{edition.title_id_low}}']),
    directTitle: savePathIdentityError({ title_id: '' }, ['{{driver.user_dir}}/{{edition.title_id}}']),
    valid: savePathIdentityError({ title_id: '0100A5B00CBD5000' }, ['{{driver.user_dir}}/{{edition.title_id_high}}{{edition.title_id_low}}']),
  }))).toEqual({
    serial: '请先为这个版本填写序列号',
    product: '请先为这个版本填写产品代码',
    splitTitle: '请先为这个版本填写 16 位十六进制标题标识',
    directTitle: '请先为这个版本填写 Title ID',
    valid: '',
  });
  for (const [locale, expected] of [
    ['zh-CN', '请先为这个版本填写 16 位十六进制标题标识'],
    ['zh-TW', '請先為這個版本填寫 16 位十六進位標題識別碼'],
    ['ja', '先にこのエディションの16桁の16進タイトル ID を入力してください'],
    ['en', 'Add the 16-digit hexadecimal title identifier to this edition first'],
  ]) {
    await page.locator('#locale').selectOption(locale);
    expect(await page.evaluate(() => tr(savePathIdentityError({ title_id: '' }, ['{{driver.user_dir}}/{{edition.title_id_high}}{{edition.title_id_low}}'])))).toBe(expected);
  }
  await page.locator('#locale').selectOption('zh-CN');
  const runtimeIconStyle = await page.locator('.runtime-catalog article>header>span svg').evaluateAll(icons => icons.map(icon => ({
    linecap: getComputedStyle(icon).strokeLinecap,
    join: getComputedStyle(icon).strokeLinejoin,
    href: icon.querySelector('use')?.getAttribute('href') || '',
  })));
  expect(runtimeIconStyle).toHaveLength(5);
  expect(runtimeIconStyle.every(icon => icon.linecap === 'square' && icon.join === 'miter' && icon.href.startsWith('#icon-runtime-'))).toBe(true);

  const gamesResponse = await page.request.get('/api/v1/games');
  const games = (await gamesResponse.json()).data;
  const game = games.find(item => item.editions.length > 0 && item.editions[0].artifacts.length > 0);
  expect(game).toBeTruthy();
  const edition = game.editions[0];

  await page.goto('/?e2e=runtime-binding#library');
  await page.locator('.management-copy .game-detail', { hasText: game.display_title }).click();
  await page.locator(`.detail-edition-row[data-edition="${edition.id}"]`).click();
  await expect(page.locator('#edition-dialog')).toBeVisible();
  await page.locator('#launch-device').selectOption('builtin-device-windows-handheld');
  await expect(page.locator('#launch-driver')).toHaveValue('builtin-driver-retroarch');
  await expect(page.locator('#launch-core option')).toContainText(['使用分层默认映射', /mGBA/]);
  await page.locator('#preview-launch-binding').click();
  await expect(page.locator('#launch-preview')).toBeVisible();
  await expect(page.locator('#launch-preview')).toContainText('mgba_libretro');
  await expect(page.locator('#launch-preview')).toContainText(edition.artifacts[0].path);

  await page.locator('#save-device').selectOption('builtin-device-windows-handheld');
  await expect(page.locator('#save-driver')).toHaveValue('builtin-driver-retroarch');
  await expect(page.locator('#save-device option:checked')).toHaveText('Windows 掌机');
  await expect(page.locator('#save-driver option:checked')).toHaveText('RetroArch');
  await expect(page.locator('#save-local-paths')).toHaveValue('{{device.save_dir}}/{{rom.stem}}.srm');
  await page.locator('#save-save-binding').click();
  await expect(page.locator('#save-binding-state')).toHaveText('已配置');
  const saveBindingsResponse = await page.request.get(`/api/v1/save-bindings?edition_id=${edition.id}`);
  expect(saveBindingsResponse.ok()).toBe(true);
  const saveBindings = (await saveBindingsResponse.json()).data;
  expect(saveBindings).toHaveLength(1);
  const saveStreamsResponse = await page.request.get(`/api/v1/save-streams?edition_id=${edition.id}`);
  expect(saveStreamsResponse.ok()).toBe(true);
  const saveStreams = (await saveStreamsResponse.json()).data;
  expect(saveStreams).toHaveLength(1);
  expect(saveBindings[0].stream_id).toBe(saveStreams[0].id);

  const bindingsResponse = await page.request.get('/api/v1/launch-bindings');
  const bindings = (await bindingsResponse.json()).data;
  expect(bindings.some(binding => binding.edition_id === edition.id && binding.device_profile_id === 'builtin-device-windows-handheld')).toBe(true);
  const resolvedResponse = await page.request.get(`/api/v1/launch-bindings/resolve?edition_id=${edition.id}&device_profile_id=builtin-device-windows-handheld`);
  const resolved = await resolvedResponse.json();
  expect(resolved.rom_path).toBe(edition.artifacts[0].path);
  expect(resolved.arguments).toEqual(['-L', 'mgba_libretro', edition.artifacts[0].path]);

  const planResponse = await page.request.post('/api/v1/package-profiles/builtin-windows-pegasus-zh/plans', { data: {} });
  expect(planResponse.ok()).toBe(true);
  const plan = await planResponse.json();
  expect(plan.status).toBe('ready');
  expect(plan.plan.conflicts).toEqual([]);
  expect(plan.plan.items.some(item => item.target === 'varkiv-launches.json' && item.action !== 'conflict')).toBe(true);
  const releaseResponse = await page.request.post(`/api/v1/package-plans/${plan.id}/build`, { data: {} });
  expect(releaseResponse.ok()).toBe(true);
  expect((await releaseResponse.json()).status).toBe('succeeded');

  const manifestPath = path.resolve(__dirname, '../../.e2e-state/exports/windows-pegasus-zh/varkiv-launches.json');
  const manifestText = fs.readFileSync(manifestPath, 'utf8');
  const manifest = JSON.parse(manifestText);
  expect(manifest.bindings.some(item => item.edition_id === edition.id && item.arguments.includes('mgba_libretro'))).toBe(true);
  expect(manifestText).not.toContain(path.resolve(__dirname, '../..'));

  await page.locator('#locale').selectOption('en');
  await expect(page.locator('#preview-launch-binding')).toHaveText('Save and preview launch command');
  await expect(page.locator('#save-launch-binding')).toHaveText('Save settings');
  await expect(page.locator('#save-save-binding')).toHaveText('Save sync settings');
  await expect(page.locator('#edition-form > footer .primary')).toHaveText('Save edition');
  await expect(page.locator('.edition-launch-panel')).not.toContainText(/[\u3400-\u9fff]/);
  await expect(page.locator('.edition-save-panel')).not.toContainText(/[\u3400-\u9fff]/);

  await page.setViewportSize({ width: 390, height: 844 });
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await expect(page.locator('.launch-binding-grid')).toHaveCSS('grid-template-columns', /\d+px/);
});

test('exact SNES bridge stays dormant until the device runtime is attested', async ({ page }) => {
  const gameResponse = await page.request.post('/api/v1/games', { data: { default_title: 'Exact save bridge fixture', platform: 'snes', titles: {} } });
  expect(gameResponse.ok()).toBe(true);
  const game = await gameResponse.json();
  const editionResponse = await page.request.post('/api/v1/editions', { data: { game_id: game.id, default_title: 'Original', edition_type: 'original' } });
  expect(editionResponse.ok()).toBe(true);
  const edition = (await editionResponse.json()).editions.find(item => item.default_title === 'Original');
  expect(edition).toBeTruthy();

  await page.goto('/?e2e=exact-save-bridge#library');
  await page.locator('.management-copy .game-detail', { hasText: game.default_title }).click();
  await page.locator(`.detail-edition-row[data-edition="${edition.id}"]`).click();
  await expect(page.locator('#edition-dialog')).toBeVisible();
  await page.locator('#save-device').selectOption('builtin-device-rocknix');
  await page.locator('#save-driver').selectOption('builtin-driver-retroarch');
  await expect(page.locator('#save-core-field')).toBeVisible();
  await page.locator('#save-core').selectOption('builtin-core-snes9x');
  await expect(page.locator('#save-binding-summary')).toHaveText('可共享；客户端核验模拟器与核心后才会参与同步。');
  await expect(page.locator('#save-binding-summary')).not.toHaveAttribute('data-tooltip', /.+/);
  await page.locator('#save-save-binding').click();
  await expect(page.locator('#save-binding-state')).toHaveText('待设备核验');
  await expect(page.locator('#save-binding-summary')).toHaveText('待客户端核验；模拟器或核心匹配前，不会读写设备存档。');
  await expect(page.locator('#save-binding-summary')).not.toHaveAttribute('data-tooltip', /.+/);

  const streamsResponse = await page.request.get(`/api/v1/save-streams?edition_id=${edition.id}`);
  const bindingsResponse = await page.request.get(`/api/v1/save-bindings?edition_id=${edition.id}`);
  expect(streamsResponse.ok()).toBe(true);
  expect(bindingsResponse.ok()).toBe(true);
  const streams = (await streamsResponse.json()).data;
  const bindings = (await bindingsResponse.json()).data;
  expect(streams).toHaveLength(1);
  expect(bindings).toHaveLength(1);
  expect(streams[0]).toMatchObject({
    driver_id: 'builtin-driver-emulatorjs-snes9x',
    compatibility_group_id: 'builtin-save-compat-snes9x-raw-srm-v1',
  });
  expect(bindings[0]).toMatchObject({
    stream_id: streams[0].id,
    driver_id: 'builtin-driver-retroarch',
    core_id: 'builtin-core-snes9x',
  });

  await page.locator('#locale').selectOption('en');
  await expect(page.locator('#save-binding-state')).toHaveText('Awaiting device verification');
  await expect(page.locator('.edition-save-panel')).not.toContainText(/[\u3400-\u9fff]/);
  await page.setViewportSize({ width: 390, height: 844 });
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await expect(page.locator('.save-binding-grid')).toHaveCSS('grid-template-columns', /\d+px/);
  const cleanupResponse = await page.request.delete(`/api/v1/games/${game.id}`);
  expect(cleanupResponse.ok()).toBe(true);
});

test('custom runtime adapters are editable, built-ins stay read-only, and deletion only removes metadata', async ({ page }) => {
  await page.goto('/?e2e=runtime-editor#settings');
  const editor = page.locator('#runtime-editor-dialog');
  const form = page.locator('#runtime-editor-form');

  await page.locator('#new-runtime-item').click();
  await form.locator('[name="kind"]').selectOption('source');
  await form.locator('[name="name"]').fill('E2E Pegasus Source');
  await form.locator('[name="source_format"]').fill('e2e-pegasus');
  await form.locator('[name="source_handler"]').selectOption('pegasus');
  await form.locator('[name="source_capabilities"]').fill('metadata=true\nmedia=true');
  await form.locator('#save-runtime-item').click();
  await expect(page.locator('#runtime-catalog button', { hasText: 'E2E Pegasus Source' })).toContainText('自定义');

  await page.locator('#new-runtime-item').click();
  await form.locator('[name="kind"]').selectOption('frontend');
  await form.locator('[name="name"]').fill('E2E Manga Frontend');
  await form.locator('[name="frontend_format"]').fill('e2e-manga');
  await form.locator('[name="frontend_handler"]').selectOption('pegasus');
  await form.locator('[name="frontend_capabilities"]').fill('export=true\ncustom_theme=true');
  await form.locator('#save-runtime-item').click();
  const customFrontend = page.locator('#runtime-catalog button', { hasText: 'E2E Manga Frontend' });
  await expect(customFrontend).toContainText('e2e-manga · 规范 v1');
  await expect(customFrontend).not.toContainText('pegasus');
  await customFrontend.click();
  await expect(form.locator('[name="frontend_handler"]')).toHaveValue('pegasus');
  await form.locator('[data-close]').first().click();

  await page.locator('#new-runtime-item').click();
  await expect(editor).toBeVisible();
  await form.locator('[name="name"]').fill('E2E RetroArch');
  await form.locator('[name="family"]').fill('e2e-retroarch');
  await form.locator('[name="platforms"]').fill('gba, snes');
  await form.locator('[name="targets"]').fill('windows, rocknix');
  await form.locator('[name="executables"]').fill('windows=retroarch.exe\nrocknix=retroarch');
  await form.locator('[name="config_paths"]').fill('settings=config\nsavedata=saves');
  await form.locator('[name="arguments"]').fill('-L\n{{core.library}}\n{{rom.path}}');
  await form.locator('[name="requires_core"]').check();
  await form.locator('[name="save_patterns"]').fill('{{device.save_dir}}/{{edition.save_namespace}}.srm');
  await form.locator('[name="save_scope_by_platform"]').fill('snes=game');
  await form.locator('[name="save_layout_by_platform"]').fill('snes=single-file');
  await form.locator('[name="save_patterns_by_platform"]').fill('snes={{device.save_dir}}/{{edition.save_namespace}}.srm');
  await form.locator('#save-runtime-item').click();
  await expect(editor).toBeHidden();
  const customDriver = page.locator('#runtime-catalog button', { hasText: 'E2E RetroArch' });
  await expect(customDriver).toContainText('自定义');

  await customDriver.click();
  await expect(form.locator('[name="platforms"]')).toHaveValue('gba, snes');
  await expect(form.locator('[name="requires_core"]')).toBeChecked();
  expect((await form.locator('[name="config_paths"]').inputValue()).split('\n').sort()).toEqual(['savedata=saves', 'settings=config']);
  await expect(form.locator('[name="save_patterns_by_platform"]')).toHaveValue('snes={{device.save_dir}}/{{edition.save_namespace}}.srm');
  await form.locator('[name="name"]').fill('E2E RetroArch Updated');
  await form.locator('#save-runtime-item').click();
  await expect(page.locator('#runtime-catalog')).toContainText('E2E RetroArch Updated');

  await page.locator('#new-runtime-item').click();
  await form.locator('[name="kind"]').selectOption('core');
  await form.locator('[name="name"]').fill('E2E Core');
  await form.locator('[name="library_names"]').fill('e2e_libretro\ne2e_alt_libretro');
  await form.locator('[name="core_platforms"]').fill('gba, gbc');
  await form.locator('#save-runtime-item').click();
  await expect(page.locator('#runtime-catalog')).toContainText('E2E Core');

  await page.locator('#new-runtime-item').click();
  await form.locator('[name="kind"]').selectOption('device');
  await form.locator('[name="name"]').fill('E2E Handheld');
  await form.locator('[name="device_target"]').fill('e2e-handheld');
  await form.locator('[name="os_family"]').fill('handheld-linux');
  await form.locator('[name="distribution"]').fill('e2e-os');
  await form.locator('[name="architecture"]').fill('aarch64');
  await form.locator('[name="device_paths"]').fill('rom_dir=roms\nsave_dir=saves\nconfig_dir=config\ncore_dir=cores\nemulator_dir=emulators');
  await form.locator('[name="default_frontend_id"]').selectOption({ label: 'Pegasus' });
  await form.locator('[name="supports_hooks"]').check();
  await form.locator('#save-runtime-item').click();
  await expect(page.locator('#runtime-catalog')).toContainText('E2E Handheld');

  await page.locator('#runtime-catalog button[data-runtime-id="builtin-frontend-pegasus"]').click();
  await expect(page.locator('#runtime-editor-title')).toHaveText('Pegasus · 前端适配器详情');
  await expect(page.locator('#runtime-evidence-level')).toHaveText('软件包已验证');
  await expect(page.locator('#runtime-evidence-scope')).toHaveText('软件夹具');
  await expect(page.locator('#runtime-evidence-date')).toHaveText('2026-08-27');
  await expect(page.locator('#runtime-evidence-contract')).toHaveText('规范版本 v5');
  await expect(form.locator('[name="frontend_capabilities"]')).toHaveValue(/neutral_manifest_v6=true/);
  await form.locator('[data-close]').first().click();

  await page.locator('#runtime-catalog button', { hasText: 'PCSX2' }).click();
  await expect(page.locator('#runtime-editor-readonly')).toBeVisible();
  await expect(page.locator('#runtime-evidence-level')).toHaveText('仅已收录');
  await expect(page.locator('#runtime-evidence-summary')).toHaveText('仅收录，尚未验证软件包或真机。');
  await expect.poll(() => form.locator('input, select, textarea').evaluateAll(controls => controls.every(control => control.disabled))).toBe(true);
  await expect(form.locator('#save-runtime-item')).toBeHidden();
  await expect(form.locator('#delete-runtime-item')).toBeHidden();
  await form.locator('[data-close]').first().click();

  await page.locator('#locale').selectOption('zh-TW');
  await expect(page.locator('#new-runtime-item')).toHaveText('新增適配');
  await page.locator('#locale').selectOption('ja');
  await expect(page.locator('#new-runtime-item')).toHaveText('連携を追加');
  await page.locator('#locale').selectOption('en');
  await expect(page.locator('#new-runtime-item')).toHaveText('Add integration');
  await page.locator('#runtime-catalog button[data-runtime-id="builtin-frontend-pegasus"]').click();
  await expect(page.locator('#runtime-editor-title')).toHaveText('Pegasus · Frontend adapter details');
  await expect(form.locator('[name="name"]')).toHaveAttribute('placeholder', 'Example: Living-room RetroArch');
  await expect(form.locator('[name="source_format"]')).toHaveAttribute('placeholder', 'Example: pegasus-custom');
  await expect(page.locator('#runtime-evidence-summary')).toHaveText('Package verified; real-device behavior is still unverified.');
  await expect(page.locator('#runtime-editor-dialog')).not.toContainText(/[\u3400-\u9fff]/);
  await form.locator('[data-close]').first().click();
  await page.locator('#locale').selectOption('zh-CN');

  await page.setViewportSize({ width: 390, height: 844 });
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await expect(page.locator('#runtime-catalog')).toHaveCSS('grid-template-columns', /\d+px/);

  const deleteNames = ['E2E Pegasus Source', 'E2E Manga Frontend', 'E2E RetroArch Updated', 'E2E Core', 'E2E Handheld'];
  for (const name of deleteNames) {
    await page.locator('#runtime-catalog button', { hasText: name }).click();
    page.once('dialog', dialog => dialog.accept());
    await form.locator('#delete-runtime-item').click();
    await expect(editor).toBeHidden();
    await expect(page.locator('#runtime-catalog')).not.toContainText(name);
  }
});

test('imported launch metadata stays inert until reviewed and applied', async ({ page }) => {
  const request = { format: 'pegasus', source: 'runtimehint/nds/metadata.pegasus.txt', platform: 'nds', locale: 'en', rom_storage: 'reference', media_storage: 'ignore' };
  const previewResponse = await page.request.post('/api/v1/imports/preview', { data: request });
  expect(previewResponse.ok()).toBe(true);
  const preview = await previewResponse.json();
  expect(preview.candidates).toHaveLength(1);
  expect(preview.candidates[0].game.runtime_hints).toHaveLength(2);
  expect(preview.candidates[0].game.runtime_hints.some(item => item.source_kind === 'structured-sidecar')).toBe(true);
  expect(preview.candidates[0].game.runtime_hints.some(item => item.raw_command?.includes('unsafe-frontend-command'))).toBe(true);
  const commitResponse = await page.request.post('/api/v1/imports/commit', { data: { ...request, preview_token: preview.preview_token, selected_tokens: [preview.candidates[0].token] } });
  expect(commitResponse.ok()).toBe(true);

  await page.goto('/?e2e=runtime-hint#library');
  await page.locator('.management-copy .game-detail', { hasText: 'Runtime Hint Demo' }).click();
  await page.locator('.detail-edition-row[data-edition="e2e-runtime-edition"]').click();
  await expect(page.locator('#runtime-import-hints')).toBeVisible();
  await expect(page.locator('.runtime-import-hint')).toHaveCount(2);
  await expect(page.locator('.runtime-import-hint.untrusted code')).toContainText('unsafe-frontend-command');
  await expect(page.locator('#runtime-import-hints')).toContainText('需人工核对，不会自动执行。');
  await expect(page.locator('.runtime-import-hint.untrusted em')).toHaveAttribute('data-tooltip', '原始命令仅供核对，Varkiv 不会执行');

  await page.locator('#locale').selectOption('en');
  await expect(page.locator('#runtime-import-hints')).not.toContainText(/[\u3400-\u9fff]/);
  await expect(page.locator('#runtime-import-hints')).toContainText('Manual review required; nothing runs automatically.');
  await expect(page.locator('.runtime-import-hint.untrusted em')).toHaveAttribute('data-tooltip', 'The original command is for review only; Varkiv does not execute it');
  await page.locator('#locale').selectOption('zh-CN');

  await page.locator('.runtime-import-hint.structured [data-review-runtime-hint]').click();
  await expect(page.locator('#launch-device')).toHaveValue('builtin-device-windows-handheld');
  await expect(page.locator('#launch-driver')).toHaveValue('builtin-driver-retroarch');
  await expect(page.locator('#launch-core')).toHaveValue('builtin-core-melonds-ds');
  await expect(page.locator('#launch-arguments')).toHaveValue('--appendconfig\n{{device.config_dir}}/nds-safe.cfg');
  await expect(page.locator('#apply-runtime-hint')).toBeVisible();
  await page.locator('#apply-runtime-hint').click();
  await expect(page.locator('.runtime-import-hint.structured')).toHaveCount(0);
  await expect(page.locator('.runtime-import-hint.untrusted')).toHaveCount(1);

  const bindingsResponse = await page.request.get('/api/v1/launch-bindings?edition_id=e2e-runtime-edition');
  expect(bindingsResponse.ok()).toBe(true);
  const bindings = (await bindingsResponse.json()).data;
  expect(bindings).toHaveLength(1);
  expect(bindings[0].arguments).toEqual(['--appendconfig', '{{device.config_dir}}/nds-safe.cfg']);
  expect(JSON.stringify(bindings[0])).not.toContain('unsafe-frontend-command');

  page.once('dialog', dialog => dialog.accept());
  await page.locator('.runtime-import-hint.untrusted [data-dismiss-runtime-hint]').click();
  await expect(page.locator('#runtime-import-hints')).toBeHidden();

  const deleteResponse = await page.request.delete('/api/v1/games/e2e-runtime-game');
  expect(deleteResponse.ok()).toBe(true);
});

test('matching runtime hints use a signed atomic batch review', async ({ page }) => {
  const request = { format: 'varkiv', source: 'runtime-batch-library-manifest.json', locale: 'en', rom_storage: 'reference', media_storage: 'ignore' };
  const previewResponse = await page.request.post('/api/v1/imports/preview', { data: request });
  expect(previewResponse.ok()).toBeTruthy();
  const preview = await previewResponse.json();
  expect(preview.candidates).toHaveLength(2);
  const commitResponse = await page.request.post('/api/v1/imports/commit', { data: { ...request, preview_token: preview.preview_token, selected_tokens: preview.candidates.map(item => item.token) } });
  expect(commitResponse.ok()).toBeTruthy();

  await page.goto('/?e2e=runtime-hint-batch#library');
  await page.locator('.management-copy .game-detail', { hasText: 'Runtime Batch One' }).click();
  await page.locator('.detail-edition-row[data-edition="e2e-runtime-batch-edition-one"]').click();
  await expect(page.locator('#runtime-import-hints')).toBeVisible();
  await page.locator('[data-review-runtime-hint]').click();
  await expect(page.locator('#preview-runtime-hint-batch')).toBeVisible();
  await expect(page.locator('#preview-runtime-hint-batch')).toContainText('2');
  await page.locator('#preview-runtime-hint-batch').click();

  const batch = page.locator('#runtime-hint-batch-preview');
  await expect(batch).toBeVisible();
  await expect(batch).toContainText('应用到同平台版本');
  await expect(batch.locator('header>b')).toHaveText('2');
  await expect(batch).toContainText('Android 掌机');
  await expect(batch).toContainText('mGBA');
  expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBe(0);

  await page.locator('#locale').selectOption('en');
  await expect(batch).toContainText('Apply to editions on this platform');
  await expect(batch).not.toContainText(/[\u3400-\u9fff]/);
  await page.locator('#locale').selectOption('zh-CN');
  await batch.locator('[data-commit-runtime-batch]').click();
  await expect(page.locator('#runtime-import-hints')).toBeHidden();
  await expect(batch).toBeHidden();

  for (const editionID of ['e2e-runtime-batch-edition-one', 'e2e-runtime-batch-edition-two']) {
    const bindingsResponse = await page.request.get(`/api/v1/launch-bindings?edition_id=${editionID}`);
    expect(bindingsResponse.ok()).toBeTruthy();
    const bindings = (await bindingsResponse.json()).data;
    expect(bindings).toHaveLength(1);
    expect(bindings[0]).toMatchObject({ device_profile_id: 'builtin-device-android-handheld', driver_id: 'builtin-driver-retroarch', core_id: 'builtin-core-mgba' });
    expect(bindings[0].arguments).toEqual(['-L', '{{core.library}}', '{{rom.path}}']);
  }
  const hintsResponse = await page.request.get('/api/v1/runtime-import-hints?status=applied');
  const appliedHints = (await hintsResponse.json()).data.filter(item => item.edition_id.startsWith('e2e-runtime-batch-edition-'));
  expect(appliedHints).toHaveLength(2);

  expect((await page.request.delete('/api/v1/games/e2e-runtime-batch-game-one')).ok()).toBeTruthy();
  expect((await page.request.delete('/api/v1/games/e2e-runtime-batch-game-two')).ok()).toBeTruthy();
});

test('browser player launches one verified Edition through a short-lived ROM capability', async ({ page }) => {
  const fixturePath = `neutral/gba/browser-player-e2e-${process.pid}.gba`;
  const fixtureFile = path.resolve(__dirname, '../../testdata', fixturePath);
  let fixtureBefore;
  let game;
  await page.addInitScript(() => {
    Object.defineProperty(navigator, 'getGamepads', { configurable: true, value: () => [{ mapping: 'standard' }] });
  });
  await page.route('https://cdn.emulatorjs.org/4.2.3/data/loader.js', async route => {
    await route.fulfill({
      status: 200,
      contentType: 'application/javascript',
      headers: { 'access-control-allow-origin': '*' },
      body: `document.querySelector('#game').innerHTML='<div id="e2e-player-ready">Emulator loader ready</div>';window.parent.postMessage({type:'varkiv:web-player-state',state:'started'},location.origin);`
    });
  });
  try {
    const fixtureBytes = Buffer.alloc(0xc0);
    fixtureBytes[0xb2] = 0x96;
    for (const value of fixtureBytes.subarray(0xa0, 0xbd)) fixtureBytes[0xbd] = (fixtureBytes[0xbd] - value) & 0xff;
    fixtureBytes[0xbd] = (fixtureBytes[0xbd] - 0x19) & 0xff;
    fs.writeFileSync(fixtureFile, fixtureBytes, { flag: 'wx', mode: 0o600 });
    fixtureBefore = fs.statSync(fixtureFile);
    const gameResponse = await page.request.post('/api/v1/games', {
      data: { default_title: 'Standalone web player fixture', platform: 'gba', titles: {} }
    });
    expect(gameResponse.ok()).toBe(true);
    game = await gameResponse.json();
    const editionResponse = await page.request.post('/api/v1/editions', {
      data: {
        game_id: game.id,
        default_title: 'Verified browser edition',
        edition_type: 'original',
        languages: [],
        titles: {},
        artifact_path: fixturePath,
        artifact_role: 'rom',
      }
    });
    expect(editionResponse.ok()).toBe(true);
    game = await editionResponse.json();
    const edition = game.editions.find(item => item.default_title === 'Verified browser edition');
    expect(edition).toBeTruthy();
    expect(edition.artifacts).toHaveLength(1);
    expect(edition.artifacts[0]).toMatchObject({ path: fixturePath, missing: false, size: fixtureBefore.size });
    expect(edition.artifacts[0].sha256).toMatch(/^[a-f0-9]{64}$/);

    await page.goto('/?e2e=web-player#library');
    await page.locator(`.game-detail[data-game="${game.id}"]`).first().click();
    const play = page.locator(`[data-web-play="${edition.id}"]`);
    await expect(play).toBeVisible();
    await expect(play).toHaveText('网页运行');
    await play.click();
    await expect(page.locator('#web-player-dialog')).toBeVisible();
    await expect(page.locator('#web-player-title')).toHaveText(edition.display_title);
    await expect(page.locator('#web-player-runtime-state')).toHaveText('运行中');
    await expect(page.locator('#web-player-runtime-state')).toHaveAttribute('data-state', 'started');
    await expect(page.locator('#web-player-input-state')).toContainText('已连接 1 个手柄');
    await expect(page.locator('#web-player-input-state')).toHaveAttribute('data-state', 'connected');
    await page.locator('.web-player-key-guide summary').click();
    await expect(page.locator('.web-player-key-panel')).toBeVisible();
    await expect(page.locator('.web-player-key-panel')).toContainText('键盘默认键位');
    await expect(page.locator('.web-player-key-panel')).toContainText('模拟器控制设置中调整映射');
    const playerFrame = page.frameLocator('#web-player-frame');
    await expect(playerFrame.locator('#e2e-player-ready')).toHaveText('Emulator loader ready');
    const playerURL = await page.locator('#web-player-frame').getAttribute('src');
    expect(playerURL).toMatch(/^\/play\/[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$/);
    expect(playerURL).not.toContain('testdata');
    expect(playerURL).not.toContain('token');
    const playerResponse = await page.request.get(playerURL);
    expect(playerResponse.ok()).toBe(true);
    const playerHTML = await playerResponse.text();
    expect(playerHTML).toContain('EJS_onSaveSave');
    expect(playerHTML).toContain('navigator.getGamepads');
    expect(playerHTML).toContain('varkiv:web-player-input');
    expect(playerHTML).not.toContain('pad.id');
    expect(playerHTML).toContain('/api/v1/web-emulation/saves/');
    expect(playerHTML).toContain('正在准备网页模拟器');
    await expect(page.locator('#web-player-dialog footer small')).toHaveText('生成存档后自动同步；不会创建空存档。');
    await page.setViewportSize({ width: 390, height: 844 });
    const frameBox = await page.locator('.web-player-frame').boundingBox();
    const footerBox = await page.locator('#web-player-dialog footer').boundingBox();
    const guideBox = await page.locator('.web-player-key-panel').boundingBox();
    expect(frameBox.height).toBeGreaterThan(500);
    expect(footerBox.height).toBeLessThan(80);
    expect(guideBox.x).toBeGreaterThanOrEqual(0);
    expect(guideBox.x + guideBox.width).toBeLessThanOrEqual(390);
    expect(guideBox.y + guideBox.height).toBeLessThanOrEqual(844);
    await page.locator('[data-close-player]').last().click();
    await expect(page.locator('#web-player-dialog')).not.toBeVisible();
    await expect(page.locator('#web-player-frame')).toHaveAttribute('src', 'about:blank');
    await expect(page.locator('#web-player-runtime-state')).toHaveAttribute('data-state', 'idle');
  } finally {
    if (game?.id) {
      const gameResponse = await page.request.get(`/api/v1/games/${game.id}`);
      if (gameResponse.ok()) expect((await page.request.delete(`/api/v1/games/${game.id}`)).ok()).toBe(true);
    }
    if (fixtureBefore) {
      const fixtureAfter = fs.statSync(fixtureFile);
      const unchanged = { size: fixtureAfter.size, mtimeMs: fixtureAfter.mtimeMs };
      fs.rmSync(fixtureFile);
      expect(unchanged).toEqual({ size: fixtureBefore.size, mtimeMs: fixtureBefore.mtimeMs });
    }
  }
});

test('NES web netplay remains available without enabling the stable browser-player asset set', async ({ page }) => {
  const fixturePath = `neutral/gba/web-netplay-e2e-${process.pid}.nes`;
  const fixtureFile = path.resolve(__dirname, '../../testdata', fixturePath);
  let game;
  let submittedBody;
  await page.addInitScript(() => localStorage.setItem('varkiv-ui-theme', 'light'));
  await page.route(/\/api\/v1\/capabilities(?:\?.*)?$/, async route => {
    const response = await route.fetch();
    const body = await response.json();
    body.features.web_netplay = true;
    await route.fulfill({ response, json: body });
  });
  await page.route(/\/api\/v1\/web-emulation\/readiness(?:\?.*)?$/, route => route.fulfill({
    status: 200, contentType: 'application/json', body: JSON.stringify({
      enabled: false, mode: 'disabled', same_origin: false, integrity_verified: false,
      assets_verified: 0, bytes_verified: 0, supported_platforms: [], supported_extensions: [], platform_capabilities: []
    })
  }));
  await page.route(/\/api\/v1\/web-netplay\/readiness(?:\?.*)?$/, route => route.fulfill({
    status: 200, contentType: 'application/json', body: JSON.stringify({
      enabled: true, experimental: true, signal_ready: true, same_origin_signal: true,
      asset_mode: 'self-hosted-verified', integrity_verified: true, emulatorjs_version: '4.3.0-pre',
      profile_id: 'emulatorjs-webrtc-v1', supported_platforms: ['nes'], save_policy: 'no-persist',
      ice_server_count: 0, assets_verified: 10, bytes_verified: 2810397,
      platform_capabilities: [{platform_id: 'nes', core: 'fceumm', extensions: ['.nes', '.unf', '.unif'], minimum_rom_bytes: 64, maximum_rom_bytes: 134217728}],
      runtime: {emulator: 'emulatorjs', version: '4.3.0-pre', core: 'fceumm', core_version: `sha256:${'a'.repeat(64)}`}
    })
  }));
  await page.route('**/api/v1/web-netplay/sessions', async route => {
    submittedBody = route.request().postDataJSON();
    await route.fulfill({status: 201, contentType: 'application/json', body: JSON.stringify({
      role: 'host', invite_code: `${'b'.repeat(32)}.${'c'.repeat(64)}`, player_url: '/e2e-web-netplay-player',
      expires_at: '2099-01-01T00:00:00Z', session: {state: 'waiting'}
    })});
  });
  await page.route('**/e2e-web-netplay-player', route => route.fulfill({
    status: 200, contentType: 'text/html', body: `<script>parent.postMessage({type:'varkiv:web-player-state',state:'started'},location.origin);parent.postMessage({type:'varkiv:web-netplay-state',state:'connected',players:2},location.origin)</script>`
  }));
  try {
    const bytes = Buffer.alloc(64);
    Buffer.from([0x4e, 0x45, 0x53, 0x1a]).copy(bytes);
    fs.writeFileSync(fixtureFile, bytes, {flag: 'wx', mode: 0o600});
    const createdGame = await page.request.post('/api/v1/games', {data: {default_title: 'Netplay fixture', platform: 'nes', titles: {}}});
    expect(createdGame.ok()).toBe(true);
    game = await createdGame.json();
    const createdEdition = await page.request.post('/api/v1/editions', {data: {
      game_id: game.id, default_title: 'Two-browser edition', edition_type: 'homebrew', languages: [], titles: {},
      artifact_path: fixturePath, artifact_role: 'rom'
    }});
    expect(createdEdition.ok()).toBe(true);
    game = await createdEdition.json();
    const edition = game.editions.find(item => item.default_title === 'Two-browser edition');

    await page.setViewportSize({width: 390, height: 844});
    await page.goto('/?e2e=web-netplay-ui#library');
    for (const [localeValue, actionLabel, submitLabel] of [
      ['zh-CN', '网页联机', '创建房间'],
      ['zh-TW', '網頁連線', '建立房間'],
      ['ja', 'Web ネットプレイ', 'ルームを作成'],
      ['en', 'Web netplay', 'Create room']
    ]) {
      await page.locator('#locale').selectOption(localeValue);
      await page.locator(`.game-detail[data-game="${game.id}"]`).first().click();
      await expect(page.locator(`[data-web-netplay="${edition.id}"]`)).toHaveText(actionLabel);
      await page.locator(`[data-web-netplay="${edition.id}"]`).click();
      await expect(page.locator('#web-netplay-form button[type="submit"]')).toContainText(submitLabel);
      await page.locator('#web-netplay-dialog [data-close-netplay]').first().click();
      await page.locator('#game-detail-dialog [data-close]').click();
    }
    await page.locator('#locale').selectOption('zh-CN');
    await page.locator(`.game-detail[data-game="${game.id}"]`).first().click();
    await expect(page.locator(`[data-web-play="${edition.id}"]`)).toHaveCount(0);
    const netplay = page.locator(`[data-web-netplay="${edition.id}"]`);
    await expect(netplay).toBeVisible();
    await expect(netplay).toHaveText('网页联机');
    await netplay.click();
    await expect(page.locator('#web-netplay-dialog')).toBeVisible();
    await expect(page.locator('#web-netplay-edition-title')).toHaveText('Two-browser edition');
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'light');
    expect(await page.locator('#web-netplay-dialog').evaluate(element => getComputedStyle(element).backgroundColor)).toBe('rgb(255, 255, 255)');
    expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBe(0);
    await page.locator('#web-netplay-form input[value="guest"]').check();
    await expect(page.locator('#web-netplay-invite-field')).toBeVisible();
    await expect(page.locator('#web-netplay-form button[type="submit"]')).toContainText('加入房间');
    await page.locator('#web-netplay-form input[value="host"]').check();
    await page.locator('#web-netplay-form input[name="display_name"]').fill('Player One');
    await page.locator('#web-netplay-form button[type="submit"]').click();
    await expect(page.locator('#web-player-dialog')).toBeVisible();
    await expect(page.locator('#web-player-netplay')).toHaveAttribute('data-state', 'connected');
    await expect(page.locator('#web-player-netplay-state')).toHaveText('联机已连接 · 2/2');
    await expect(page.locator('#web-player-invite-code')).toBeVisible();
    await expect(page.locator('#web-player-invite-code')).toHaveText(`${'b'.repeat(32)}.${'c'.repeat(64)}`);
    await expect(page.locator('#copy-web-player-invite')).toBeVisible();
    const playerFrameBox = await page.locator('.web-player-frame').boundingBox();
    expect(playerFrameBox?.height).toBeGreaterThan(500);
    expect(playerFrameBox?.width).toBeLessThanOrEqual(390);
    expect(submittedBody).toMatchObject({edition_id: edition.id, display_name: 'Player One', locale: 'zh-CN'});
    expect(submittedBody.client_id).toBeTruthy();
    await page.getByRole('button', {name: '退出运行', exact: true}).click();
    await expect(page.locator('#web-player-dialog')).not.toBeVisible();
    await expect(page.locator('#web-player-invite-code')).toBeHidden();
    await expect(page.locator('#web-player-invite-code')).toHaveText('');
  } finally {
    if (game?.id) expect((await page.request.delete(`/api/v1/games/${game.id}`)).ok()).toBe(true);
    if (fs.existsSync(fixtureFile)) fs.rmSync(fixtureFile);
  }
});

test('web netplay UI fails closed and presents localized join failures without launching a player', async ({ page }) => {
  const fixturePath = `neutral/gba/web-netplay-errors-${process.pid}.nes`;
  const fixtureFile = path.resolve(__dirname, '../../testdata', fixturePath);
  let game;
  let signalReady = false;
  let joinFailure = {status: 409, code: 'compatibility_mismatch', message: 'content.sha256'};
  await page.route(/\/api\/v1\/capabilities(?:\?.*)?$/, async route => {
    const response = await route.fetch();
    const body = await response.json();
    body.features.web_netplay = true;
    await route.fulfill({response, json: body});
  });
  await page.route(/\/api\/v1\/web-netplay\/readiness(?:\?.*)?$/, route => route.fulfill({
    status: 200, contentType: 'application/json', body: JSON.stringify({
      enabled: true, experimental: true, signal_ready: signalReady, same_origin_signal: true,
      asset_mode: 'self-hosted-verified', integrity_verified: true, emulatorjs_version: '4.3.0-pre',
      profile_id: 'emulatorjs-webrtc-v1', supported_platforms: ['nes'], save_policy: 'no-persist',
      ice_server_count: 0, assets_verified: 10, bytes_verified: 2810397,
      platform_capabilities: [{platform_id: 'nes', core: 'fceumm', extensions: ['.nes'], minimum_rom_bytes: 64, maximum_rom_bytes: 134217728}],
      runtime: {emulator: 'emulatorjs', version: '4.3.0-pre', core: 'fceumm', core_version: `sha256:${'a'.repeat(64)}`}
    })
  }));
  await page.route('**/api/v1/web-netplay/sessions/join', route => route.fulfill({
    status: joinFailure.status, contentType: 'application/json', body: JSON.stringify({error: {code: joinFailure.code, message: joinFailure.message}})
  }));
  try {
    const bytes = Buffer.alloc(64);
    Buffer.from([0x4e, 0x45, 0x53, 0x1a]).copy(bytes);
    fs.writeFileSync(fixtureFile, bytes, {flag: 'wx', mode: 0o600});
    const createdGame = await page.request.post('/api/v1/games', {data: {default_title: 'Netplay failures', platform: 'nes', titles: {}}});
    expect(createdGame.ok()).toBe(true);
    game = await createdGame.json();
    const createdEdition = await page.request.post('/api/v1/editions', {data: {
      game_id: game.id, default_title: 'Failure edition', edition_type: 'homebrew', languages: [], titles: {},
      artifact_path: fixturePath, artifact_role: 'rom'
    }});
    expect(createdEdition.ok()).toBe(true);
    game = await createdEdition.json();
    const edition = game.editions.find(item => item.default_title === 'Failure edition');

    await page.setViewportSize({width: 390, height: 844});
    await page.goto('/?e2e=web-netplay-errors#library');
    await page.locator(`.game-detail[data-game="${game.id}"]`).first().click();
    await expect(page.locator(`[data-web-netplay="${edition.id}"]`)).toHaveCount(0);
    signalReady = true;
    await page.reload();
    await page.locator(`.game-detail[data-game="${game.id}"]`).first().click();
    await page.locator(`[data-web-netplay="${edition.id}"]`).click();
    await page.locator('#web-netplay-form input[name="display_name"]').fill('Host');
    await page.locator('#web-netplay-form button[type="submit"]').click();
    await expect(page.locator('#notice.error')).toHaveText('网页联机服务暂不可用。');
    await expect(page.locator('#web-netplay-dialog')).toBeVisible();
    await expect(page.locator('#web-player-dialog')).not.toBeVisible();
    await page.locator('#web-netplay-form input[value="guest"]').check();
    await page.locator('#web-netplay-form input[name="display_name"]').fill('Guest');
    await page.locator('#web-netplay-form input[name="invite_code"]').fill(`${'b'.repeat(32)}.${'c'.repeat(64)}`);
    await page.locator('#web-netplay-form button[type="submit"]').click();
    await expect(page.locator('#notice.error')).toHaveText('ROM 或网页模拟器版本与房主不一致。');
    await expect(page.locator('#web-player-dialog')).not.toBeVisible();
    await expect(page.locator('#web-netplay-dialog')).toBeVisible();

    joinFailure = {status: 401, code: 'invalid_invitation', message: 'invalid or expired invitation'};
    await page.locator('#web-netplay-form button[type="submit"]').click();
    await expect(page.locator('#notice.error')).toHaveText('邀请口令无效或已过期。');
    expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBe(0);
    page.clientErrors = page.clientErrors.filter(message => ![
      'console: Failed to load resource: the server responded with a status of 503 (Service Unavailable)',
      'console: Failed to load resource: the server responded with a status of 409 (Conflict)',
      'console: Failed to load resource: the server responded with a status of 401 (Unauthorized)'
    ].includes(message));
  } finally {
    if (game?.id) expect((await page.request.delete(`/api/v1/games/${game.id}`)).ok()).toBe(true);
    if (fs.existsSync(fixtureFile)) fs.rmSync(fixtureFile);
  }
});

test('metadata entries whose ROM is missing are visible but disabled and skipped', async ({ page }) => {
  await openTransfer(page);
  const gamesBefore = (await (await page.request.get('/api/v1/games')).json()).data.length;
  const metadataKind = page.locator('input[name="import_kind"][value="metadata"]');
  await metadataKind.locator('xpath=..').click();
  await expect(metadataKind).toBeChecked();
  const esdeFormat = page.locator('input[name="format"][value="es-de"]');
  await esdeFormat.locator('xpath=..').click();
  await expect(esdeFormat).toBeChecked();
  await page.locator('input[name="source"]').fill('missing/gamelist.xml');
  await choosePlatform(page);
  await page.locator('#preview-import').click();

  await expect(page.locator('#import-review')).toBeVisible();
  await expect(page.locator('#import-preview .status-pill.missing')).toHaveCount(1);
  await expect(page.locator('#import-preview .preview-item input')).toBeDisabled();
  await expect(page.locator('#import-summary')).toContainText('缺失并跳过');
  await expect(page.locator('#import-preview .status-pill.missing')).toHaveAttribute('data-tooltip', /此条目会跳过/);
  await expect(page.locator('#commit-import')).toBeHidden();
  const savedState = page.locator('#import-source-state');
  await expect(savedState).toBeVisible();
  await expect(savedState).toContainText('来源已保存，可稍后重扫');
  await expect(page.locator('#commit-summary')).toHaveText('等待 ROM 文件');
  await expect(page.locator('#commit-note')).toContainText('不会创建空游戏');
  const sources = (await (await page.request.get('/api/v1/sources')).json()).data;
  const savedSource = sources.find(source => source.metadata_path === 'missing/gamelist.xml');
  expect(savedSource).toMatchObject({ kind: 'esde', platform: 'gba', enabled: true });
  const scans = (await (await page.request.get(`/api/v1/source-scans?source_id=${savedSource.id}`)).json()).data;
  expect(scans.at(-1)).toMatchObject({ status: 'ready', candidate_count: 1, importable_count: 0, missing_count: 1 });
  expect((await (await page.request.get('/api/v1/games')).json()).data).toHaveLength(gamesBefore);

  const expected = {
    'zh-TW': '來源已儲存，可稍後重新掃描',
    ja: 'ソースを保存しました。後で再スキャンできます',
    en: 'Source saved for rescanning',
  };
  for (const [language, text] of Object.entries(expected)) {
    await page.locator('#locale').selectOption(language);
    await expect(savedState.locator('strong')).toHaveText(text);
  }
  await page.locator('#locale').selectOption('zh-CN');
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(savedState).toBeVisible();
  expect(await savedState.evaluate(element => element.scrollWidth - element.clientWidth)).toBeLessThanOrEqual(1);
  expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBeLessThanOrEqual(1);
});

test('wrapped source diagnosis is concise, private, progressive, and localized', async ({ page }) => {
  await openTransfer(page);
  const preview = {
    parsed: 1,
    preview_token: 'synthetic-preview-token',
    source_diagnostics: [
      { code: 'wrapped_archives_detected', count: 1 },
      { code: 'split_archives_detected', count: 2 },
      { code: 'platform_wrapped_archives_detected', count: 1 },
      { code: 'platform_split_archive_parts_detected', count: 2 },
    ],
    candidates: [{
      index: 0,
      status: 'missing',
      availability: 'missing',
      available_artifacts: 0,
      missing_artifacts: 1,
      token: 'synthetic-candidate-token',
      game: { default_title: 'Synthetic missing game', edition_title: 'Synthetic missing game', platform: 'gba', artifacts: [{ path: 'missing.gba', missing: true }], media: [] },
    }],
  };
  const expected = {
    'zh-CN': ['当前平台仍在封装中', '发现 3 个与当前平台匹配的封装文件。', '怎么处理'],
    'zh-TW': ['目前平台仍在封裝中', '發現 3 個符合目前平台的封裝檔案。', '如何處理'],
    ja: ['この機種はまだコンテナ内です', 'この機種に一致するコンテナファイルを 3 件検出しました。', '対処方法'],
    en: ['This platform is still packaged', 'Found 3 packaged files matching this platform.', 'How to fix this'],
  };
  for (const language of Object.keys(expected)) {
    await page.locator('#locale').selectOption(language);
    await page.evaluate(value => renderImportPreview(value), preview);
    const diagnostic = page.locator('#import-source-diagnostics');
    await expect(diagnostic).toBeVisible();
    await expect(diagnostic.locator('header strong')).toHaveText(expected[language][0]);
    await expect(diagnostic.locator('header small')).toHaveText(expected[language][1]);
    await expect(diagnostic.locator('summary')).toHaveText(expected[language][2]);
    await expect(diagnostic.locator('details')).not.toHaveAttribute('open', '');
    await expect(diagnostic.locator('p')).not.toBeVisible();
    await diagnostic.locator('summary').click();
    await expect(diagnostic.locator('p')).toBeVisible();
    await expect(diagnostic).not.toContainText('private-platform');
  }
  const unmatched = {
    'zh-CN': ['ROM 内容目录可能不匹配', '发现 3 个封装文件，但未识别到当前平台。'],
    'zh-TW': ['ROM 內容目錄可能不符', '發現 3 個封裝檔案，但未識別到目前平台。'],
    ja: ['ROM コンテンツフォルダーが一致しない可能性があります', 'コンテナファイルを 3 件検出しましたが、この機種とは判定できませんでした。'],
    en: ['The ROM content folder may not match', 'Found 3 packaged files, but none were identified for this platform.'],
  };
  for (const language of Object.keys(unmatched)) {
    await page.locator('#locale').selectOption(language);
    await page.evaluate(value => renderImportPreview({ ...value, source_diagnostics: value.source_diagnostics.slice(0, 2) }), preview);
    const diagnostic = page.locator('#import-source-diagnostics');
    await expect(diagnostic.locator('header strong')).toHaveText(unmatched[language][0]);
    await expect(diagnostic.locator('header small')).toHaveText(unmatched[language][1]);
    await expect(diagnostic.locator('details')).not.toHaveAttribute('open', '');
  }
});

test('neutral v4 manifest imports directly, keeps per-entry platforms, and skips missing ROMs', async ({ page }) => {
  await openTransfer(page);
  const metadataKind = page.locator('input[name="import_kind"][value="metadata"]');
  await metadataKind.locator('xpath=..').click();
  const neutralFormat = page.locator('input[name="format"][value="varkiv"]');
  await neutralFormat.locator('xpath=..').click();
  await expect(neutralFormat).toBeChecked();
  await expect(page.locator('#import-platform-field')).toBeHidden();
  await expect(page.locator('#remember-source-field')).toBeVisible();
  await page.locator('input[name="remember_source"]').uncheck();
  await expect(page.locator('#detected-sources')).toContainText('neutral/library-manifest.json');
  await page.locator('.detected-source', { hasText: 'neutral/library-manifest.json' }).click();
  await expect(page.locator('input[name="source"]')).toHaveValue('neutral/library-manifest.json');
  await page.locator('#preview-import').click();

  await expect(page.locator('#import-preview .preview-item')).toHaveCount(2);
  await expect(page.locator('#import-preview .status-pill.new')).toHaveCount(1);
  await expect(page.locator('#import-preview .status-pill.missing')).toHaveCount(1);
  await expect(page.locator('#import-preview input:checked')).toHaveCount(1);
  await page.locator('#commit-import').click();
  await expect(page.locator('#import-inline-state')).toContainText('成功导入 1 项，跳过 1 项');

  const gameResponse = await page.request.get('/api/v1/games/e2e-neutral-game?locale=en');
  expect(gameResponse.ok()).toBe(true);
  const game = await gameResponse.json();
  expect(game.platform).toBe('gba');
  expect(game.editions).toHaveLength(1);
  expect(game.editions[0].id).toBe('e2e-neutral-edition');
  expect(game.editions[0].artifacts[0].sha256).toMatch(/^[a-f0-9]{64}$/);
  const hintsResponse = await page.request.get('/api/v1/runtime-import-hints?edition_id=e2e-neutral-edition');
  expect(hintsResponse.ok()).toBe(true);
  const hints = (await hintsResponse.json()).data;
  expect(hints).toHaveLength(1);
  expect(hints[0].trust).toBe('structured');
  expect(hints[0].status).toBe('pending');

  expect((await page.request.get('/api/v1/games/e2e-neutral-missing-game')).status()).toBe(404);
  expect((await page.request.delete('/api/v1/games/e2e-neutral-game')).ok()).toBe(true);
  expect((await page.request.delete('/api/v1/series/e2e-neutral-series')).ok()).toBe(true);
});

test('neutral v6 preview explains portable custom platforms before atomic import', async ({ page }) => {
  await openTransfer(page);
  const metadataKind = page.locator('input[name="import_kind"][value="metadata"]');
  await metadataKind.locator('xpath=..').click();
  const neutralFormat = page.locator('input[name="format"][value="varkiv"]');
  await neutralFormat.locator('xpath=..').click();
  await page.locator('input[name="remember_source"]').uncheck();
  await page.locator('input[name="source"]').fill('portable-v6/library-manifest.json');
  await page.locator('#preview-import').click();

  const platformNotice = page.locator('#import-platform-changes');
  await expect(platformNotice).toBeVisible();
  await expect(platformNotice).toContainText('整合包携带自定义平台定义');
  await expect(platformNotice).toContainText('便携测试平台');
  await expect(platformNotice).toContainText('portable-v6');
  await expect(platformNotice).toContainText('将随所选条目创建');
  await expect(page.locator('#import-preview .preview-platform')).toHaveText('便携测试平台');

  const candidate = page.locator('#import-preview input');
  await candidate.uncheck();
  await expect(platformNotice).toBeHidden();
  await candidate.check();
  await expect(platformNotice).toBeVisible();

  await page.locator('#locale').selectOption('en');
  await expect(platformNotice).toContainText('Package includes custom platform definitions');
  await expect(platformNotice).toContainText('Portable V6');
  await expect(platformNotice).toContainText('Create with selected items');
  await expect(page.locator('#import-review')).not.toContainText(/[\u3400-\u9fff]/);
  await page.locator('#locale').selectOption('zh-CN');

  await page.locator('#commit-import').click();
  await expect(page.locator('#import-inline-state')).toContainText('成功导入 1 项');
  await expect(platformNotice).toBeHidden();
  const platformResponse = await page.request.get('/api/v1/custom-platforms/portable-v6');
  expect(platformResponse.ok()).toBe(true);
  const platform = await platformResponse.json();
  expect(platform.name).toBe('Portable V6');
  expect(platform.enabled).toBe(true);
  expect((await page.request.get('/api/v1/games/e2e-portable-v6-game')).ok()).toBe(true);

  expect((await page.request.delete('/api/v1/games/e2e-portable-v6-game')).ok()).toBe(true);
  expect((await page.request.delete('/api/v1/custom-platforms/portable-v6')).ok()).toBe(true);
});

test('runtime v2 preview restores custom runtime definitions and package templates atomically', async ({ page }) => {
  await openTransfer(page);
  await page.locator('input[name="import_kind"][value="metadata"]').locator('xpath=..').click();
  await page.locator('input[name="format"][value="varkiv"]').locator('xpath=..').click();
  await page.locator('input[name="remember_source"]').uncheck();
  await page.locator('input[name="source"]').fill('portable-runtime-v2/library-manifest.json');
  await page.locator('#preview-import').click();

  const notice = page.locator('#import-runtime-changes');
  await expect(notice).toBeVisible();
  await expect(notice).toContainText('整合包携带可恢复的运行配置');
  await expect(notice).toContainText('Portable manga metadata');
  await expect(notice).toContainText('Portable runtime test device');
  await expect(notice).toContainText('Portable runtime test RetroArch');
  await expect(notice).toContainText('Portable runtime test mGBA');
  await expect(notice).toContainText('Portable runtime V2 package');
  await expect(notice.locator('article')).toHaveCount(5);

  const candidate = page.locator('#import-preview input');
  await candidate.uncheck();
  await expect(notice).toBeHidden();
  await candidate.check();
  await page.locator('#locale').selectOption('en');
  await expect(notice).toContainText('Package includes recoverable runtime configuration');
  await expect(notice).toContainText('Create with selected items');
  await expect(notice).not.toContainText(/[\u3400-\u9fff]/);
  await page.locator('#locale').selectOption('zh-CN');

  await page.locator('#commit-import').click();
  await expect(page.locator('#import-inline-state')).toContainText('成功导入 1 项');
  await expect(notice).toBeHidden();
  for (const path of [
    '/api/v1/frontend-adapters/e2e-runtime-v2-frontend',
    '/api/v1/device-profiles/e2e-runtime-v2-device',
    '/api/v1/emulator-drivers/e2e-runtime-v2-driver',
    '/api/v1/retroarch-cores/e2e-runtime-v2-core',
    '/api/v1/package-profiles/e2e-runtime-v2-profile',
  ]) expect((await page.request.get(path)).ok()).toBe(true);
  const profile = await (await page.request.get('/api/v1/package-profiles/e2e-runtime-v2-profile')).json();
  expect(profile.frontend_adapter_id).toBe('e2e-runtime-v2-frontend');
  expect(profile.templates).toHaveLength(1);
  expect(profile.templates[0].body).toContain('core={{core.library}}');
  const hints = (await (await page.request.get('/api/v1/runtime-import-hints?edition_id=e2e-runtime-v2-edition')).json()).data;
  expect(hints).toHaveLength(1);
  expect(hints[0].source_format).toBe('varkiv-launches-v2');

  expect((await page.request.delete('/api/v1/games/e2e-runtime-v2-game')).ok()).toBe(true);
  expect((await page.request.delete('/api/v1/package-profiles/e2e-runtime-v2-profile')).ok()).toBe(true);
  expect((await page.request.delete('/api/v1/emulator-drivers/e2e-runtime-v2-driver')).ok()).toBe(true);
  expect((await page.request.delete('/api/v1/retroarch-cores/e2e-runtime-v2-core')).ok()).toBe(true);
  expect((await page.request.delete('/api/v1/device-profiles/e2e-runtime-v2-device')).ok()).toBe(true);
  expect((await page.request.delete('/api/v1/frontend-adapters/e2e-runtime-v2-frontend')).ok()).toBe(true);
});

test('transfer surface localizes into every supported interface language', async ({ page }) => {
  await openTransfer(page);
  const locale = page.locator('#locale');
  const previewButton = page.locator('#preview-import span');

  await locale.selectOption('zh-TW');
	await expect(previewButton).toHaveText('掃描並預覽');
  await expect(page.locator('.sidebar nav a[data-view]')).toHaveText(['資料庫', '平台目錄', '匯入來源', '整合包', '存檔同步', '系統設定']);
  await expect(page.locator('#sources-view h1')).toHaveText('匯入來源');
  await locale.selectOption('ja');
  await expect(previewButton).toHaveText('スキャンして確認');
  await expect(page.locator('.sidebar nav a[data-view]')).toHaveText(['ライブラリ', '機種カタログ', 'インポート元', 'パッケージ', 'セーブ同期', 'システム設定']);
  await expect(page.locator('#sources-view h1')).toHaveText('インポート元');
  await locale.selectOption('en');
  await expect(previewButton).toHaveText('Scan and preview');
  await expect(page.locator('.sidebar nav a[data-view]')).toHaveText(['Library', 'Platform catalog', 'Import sources', 'Packages', 'Save sync', 'System settings']);
  await expect(page.locator('#sources-view h1')).toHaveText('Import sources');
  expect(await page.locator('[placeholder]').evaluateAll(elements => elements.map(element => element.getAttribute('placeholder')).filter(value => /[\u3400-\u9fff]/.test(value || '')))).toEqual([]);
  await expect(page.locator('#import-platform-preset option[value="gba"]')).toHaveText('Nintendo Game Boy Advance · gba');
  await expect(page.locator('#source-registry-title')).toHaveText('Saved sources');
  await page.locator('input[name="import_kind"][value="metadata"]').locator('xpath=..').click();
  await expect(page.locator('#content-root-field')).toContainText('ROM folder (optional)');
  await expect(page.locator('#content-root-field')).not.toContainText(/[\u3400-\u9fff]/);
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  expect(await page.locator('.import-source-card, #import-form, .import-kind-choice, .import-kind-option').evaluateAll(elements => elements
    .filter(element => element.scrollWidth > element.clientWidth + 1)
    .map(element => ({ id: element.id, className: element.className })))).toEqual([]);
  await page.locator('a[href="#packages"]').click();
  await expect(page.locator('#profile-grid .profile-option')).toHaveCount(8);
	await expect(page.locator('#profile-grid')).toContainText('SteamOS / Bazzite handheld');
  await expect(page.locator('#profile-grid')).toContainText('dArkOS handheld');
  await expect(page.locator('#profile-grid')).toContainText('ArkOS handheld (legacy)');
  await expect(page.locator('#profile-grid')).toContainText('KNULLI handheld');
  await expect(page.locator('#profile-grid .profile-option').last()).toContainText('ArkOS handheld (legacy)');
  await expect(page.locator('#profile-grid')).not.toContainText(/muOS|OnionOS/);
  await expect(page.locator('#profile-grid')).not.toContainText(/[\u3400-\u9fff]/);
  await page.locator('a[href="#settings"]').click();
  await expect(page.locator('#settings-view h1')).toHaveText('System settings');
  await expect(page.locator('.settings-overview')).not.toContainText(/[\u3400-\u9fff]/);
  await page.locator('a[href="#platforms"]').click();
  await expect(page.locator('#platforms-view h1')).toHaveText('Platform catalog');
  await expect(page.locator('#platforms-view .platform-command-copy')).not.toContainText(/[\u3400-\u9fff]/);
  await page.goto('/?e2e=locales#library');
  await expect(page.locator('[data-library-mode="list"]')).toHaveText(/List view/);
  await expect(page.locator('[data-library-mode="covers"]')).toHaveText(/Cover view/);
  await expect(page.locator('[data-library-mode="series"]')).toHaveText(/Series view/);
  await expect(page.locator('.management-head')).toContainText('Games');
  await expect(page.locator('.management-head')).toContainText('ROM');
  await expect(page.locator('.management-files .health-state.ready').first()).toHaveText(/Files healthy/);
  await expect(page.locator('.management-files .health-state.unlinked')).toHaveText(/No ROM linked/);
  await page.locator('#new-game').click();
  await expect(page.locator('#game-dialog [name="zh-CN"]')).toHaveAttribute('placeholder', 'Final Fantasy VI');
  await page.locator('#game-dialog [data-close]').first().click();
  await page.locator('[data-library-mode="series"]').click();
  await page.locator('#new-game').click();
  await expect(page.locator('#series-dialog [name="s-zh-CN"]')).toHaveAttribute('placeholder', 'The Legend of Zelda');
  await page.locator('#series-dialog [data-close]').first().click();
  await page.locator('[data-library-mode="list"]').click();
  await page.locator('.management-row .game-edit').first().click();
  await expect(page.locator('#game-media-panel')).toBeVisible();
  await expect(page.locator('#game-media-panel .media-file-picker strong')).toHaveText('Choose file');
  await expect(page.locator('#game-media-file-name')).toHaveText('No file selected');
  await expect(page.locator('#game-media-panel .recheck-media')).toHaveText('Recheck media');
  await expect(page.locator('#game-media .media-content-status').first()).toHaveText('File available');
  await expect(page.locator('#merge-source option').first()).toContainText(/\d+ edition/);
  await page.locator('#game-media .edit-media-meta').first().click();
  await expect(page.locator('#game-media .media-edit-panel').first()).toContainText('Media type');
  await expect(page.locator('#game-media .media-edit-panel').first()).toContainText('Changes classification only; the file and its owner stay unchanged.');
  expect((await page.locator('#game-media .media-edit-panel').first().textContent()).replace('日本語', '')).not.toMatch(/[\u3400-\u9fff]/);
  await page.locator('#game-media .cancel-media-edit').first().click();
  expect((await page.locator('#game-media-panel').textContent()).replace('日本語', '')).not.toMatch(/[\u3400-\u9fff]/);
  await page.locator('#game-form [data-close]').first().click();
  await page.locator('.management-title .game-detail').first().click();
  await expect(page.locator('.game-detail-dialog')).toBeVisible();
  await expect(page.locator('.detail-save-state')).toContainText('No saves yet');
  await expect(page.locator('.game-detail-dialog')).not.toContainText(/[\u3400-\u9fff]/);
  await page.locator('.detail-edition-row').first().click();
  await expect(page.locator('#edition-dialog')).toBeVisible();
  await page.locator('.artifact-row .edit-artifact').first().click();
  await expect(page.locator('.artifact-edit-panel').first()).toContainText('Resource type');
  await expect(page.locator('.artifact-edit-panel').first()).toContainText('Changes category and disc number only; the ROM file stays unchanged.');
  await expect(page.locator('.artifact-edit-panel').first()).not.toContainText(/[\u3400-\u9fff]/);
  await page.locator('#edition-dialog [data-close]').first().click();
  await locale.selectOption('ja');
  await expect(page.locator('[data-library-mode="list"]')).toHaveText(/リスト表示/);
  await locale.selectOption('zh-TW');
  await expect(page.locator('[data-library-mode="list"]')).toHaveText(/目錄檢視/);
  await locale.selectOption('zh-CN');
  await expect(page.locator('[data-library-mode="list"]')).toHaveText(/目录视图/);
});

test('ROM identity exchange previews provenance, imports, exports, and stays usable on phone', async ({ page }) => {
  let imported = false;
  await page.route(/\/api\/v1\/hash-sources(?:\?.*)?$/, route => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({ data: imported ? [{
      id: 'community.fixture', name: 'Community Fixture', publisher: 'Fixture authors', license: 'CC0-1.0',
      trust_level: 'imported', active_release_id: 'release-fixture', active_version: '2026.09',
      active_pack_sha256: 'a'.repeat(64), record_count: 2, release_count: 1,
      created_at: '2026-09-02T00:00:00Z', updated_at: '2026-09-02T00:00:00Z',
    }] : [], pagination: { limit: 100, offset: 0, total: imported ? 1 : 0 } }),
  }));
  await page.route('**/api/v1/hash-packs/preview', async route => {
    expect(route.request().method()).toBe('POST');
    expect(route.request().postDataBuffer().toString()).toContain('fixture.hashpack');
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({
      source: { id: 'community.fixture', name: 'Community Fixture', publisher: 'Fixture authors', license: 'CC0-1.0' },
      release: '2026.09', pack_id: 'b'.repeat(64), pack_sha256: 'a'.repeat(64), record_count: 2,
      new_count: 1, existing_count: 0, conflict_count: 1, existing_release: false, release_conflict: false,
      preview_token: 'signed-preview-fixture',
    }) });
  });
  await page.route('**/api/v1/hash-packs/import', async route => {
    const payload = route.request().postDataBuffer().toString();
    expect(payload).toContain('fixture.hashpack');
    expect(payload).toContain('signed-preview-fixture');
    imported = true;
    await route.fulfill({ status: 201, contentType: 'application/json', body: JSON.stringify({
      source: { id: 'community.fixture', name: 'Community Fixture', license: 'CC0-1.0', trust_level: 'imported', active_release_id: 'release-fixture', active_version: '2026.09', active_pack_sha256: 'a'.repeat(64), record_count: 2, release_count: 1, created_at: '2026-09-02T00:00:00Z', updated_at: '2026-09-02T00:00:00Z' },
      release: { id: 'release-fixture', source_id: 'community.fixture', version: '2026.09', format_version: 1, pack_id: 'b'.repeat(64), pack_sha256: 'a'.repeat(64), records_sha256: 'c'.repeat(64), record_count: 2, source_name: 'Community Fixture', license: 'CC0-1.0', active: true, imported_at: '2026-09-02T00:00:00Z' },
      imported_records: 2, existing_release: false,
    }) });
  });
  await page.route('**/api/v1/hash-packs/export', async route => {
    expect(route.request().postDataJSON()).toMatchObject({ source_id: 'personal.fixture', release: '1', license: 'CC0-1.0' });
    await route.fulfill({ status: 200, headers: { 'Content-Type': 'application/vnd.varkiv.hashpack+zip', 'Content-Disposition': 'attachment; filename="personal.fixture-1.hashpack"' }, body: Buffer.from('PK fixture') });
  });

  await page.setViewportSize({ width: 390, height: 844 });
  await openTransfer(page);
  await expect(page.locator('.identity-exchange')).toBeVisible();
  await page.locator('#locale').selectOption('en');
  await expect(page.locator('.identity-exchange h2')).toHaveText('ROM identity library');
  await expect(page.locator('.hash-privacy')).toContainText('Excludes ROMs, paths, filenames, media, saves, devices, and play history.');
  await page.locator('#hash-pack-file').setInputFiles({ name: 'fixture.hashpack', mimeType: 'application/vnd.varkiv.hashpack+zip', buffer: Buffer.from('PK fixture') });
  await page.locator('#preview-hash-pack').click();
  await expect(page.locator('#hash-pack-preview')).toContainText('Sources disagree; existing metadata will not be overwritten');
  await expect(page.locator('#hash-pack-preview')).toContainText('1New identities');
  await page.locator('#commit-hash-pack').click();
  await expect(page.locator('.hash-source-chip')).toContainText('Community Fixture');
  await expect(page.locator('.hash-source-chip')).toContainText('1 release');

  await page.locator('#hash-pack-export-form input[name="source_id"]').fill('personal.fixture');
  await page.locator('#hash-pack-export-form input[name="release"]').fill('1');
  await page.locator('#hash-pack-export-form input[name="name"]').fill('Personal fixture');
  const downloadPromise = page.waitForEvent('download');
  await page.locator('#export-hash-pack').click();
  const download = await downloadPromise;
  expect(download.suggestedFilename()).toBe('personal.fixture-1.hashpack');
  expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBe(0);
  expect(await page.locator('.identity-exchange, .hash-pack-panel, .hash-pack-picker, #hash-pack-export-form input').evaluateAll(elements => elements
    .filter(element => element.scrollWidth > element.clientWidth + 1)
    .map(element => ({ id: element.id, className: element.className })))).toEqual([]);
});

test('save sync is a client status surface linked to ROM editions', async ({ page }) => {
  await page.goto('/?e2e=save-sync#sync');
  await expect(page.locator('#devices-view')).toBeVisible();
  await expect(page.locator('#devices-view h1')).toHaveText('存档同步');
  await expect(page.locator('#device-form, #save-form, #devices-view input[type="file"]')).toHaveCount(0);
  await expect(page.locator('#sync-overview')).toBeVisible();
  await expect(page.locator('#sync-overview')).toHaveCSS('border-top-width', '1px');
  await expect(page.locator('#sync-overview > div').first()).toHaveCSS('border-radius', '0px');
  await expect(page.locator('.sync-agent-icon')).toHaveCount(0);
  await expect(page.locator('.sync-agent-head .section-number')).toHaveText('01');
  await expect(page.locator('.sync-agent-head')).toContainText('设备客户端');
  await expect(page.locator('#devices-view .sync-panel .section-number')).toHaveText(['02', '03', '04', '05']);
  await expect(page.locator('#sync-coverage .coverage-row')).toHaveCount(3);
  await expect(page.locator('#sync-coverage .coverage-head')).toHaveText(/游戏版本.*平台.*ROM 识别.*同步状态/);
  await expect(page.locator('#sync-coverage')).toContainText('1 / 1 个指纹');
  await expect(page.locator('#sync-coverage')).toContainText('Game Boy Advance');
  await expect(page.locator('#sync-coverage')).not.toContainText('命名空间');
  await expect(page.locator('.agent-boundary')).toHaveText('无需手动上传存档');
  await expect(page.locator('#devices-view')).not.toContainText('varkiv agent run');
  await expect(page.locator('#pair-device-profile option')).toHaveCount(10);
  await expect(page.locator('#pair-device-profile option').first()).toHaveText('Android 掌机');
  await expect(page.locator('#pair-device-profile')).not.toContainText(/ · (android|windows|rocknix|portable)/);
  await page.locator('#pair-device-profile').selectOption('builtin-device-windows-handheld');
  await page.locator('#issue-pairing-code').click();
  await expect(page.locator('#pairing-code-result')).toBeVisible();
  await expect(page.locator('#pairing-code-result > strong')).toHaveText(/^[A-Z0-9]{5}-[A-Z0-9]{5}$/);
  await expect(page.locator('#pairing-code-result code')).toContainText('varkiv agent pair');
  await expect(page.locator('#pairing-code-result code')).not.toContainText('--profile');
  await expect(page.locator('#pairing-code-result code')).not.toContainText('<DEVICE_ROOT>');
  await expect(page.locator('#pairing-code-result code')).toContainText('--root "C:\\Varkiv"');
  await expect(page.locator('#pairing-code-result small')).toHaveText('10 分钟内在设备上完成配对；令牌只返回一次');
  await expect(page.locator('#pairing-code-result button')).toHaveText('复制命令');
  await expect(page.locator('#pairing-code-result code')).toContainText('--name "我的掌机"');
  const pairingLocale = page.locator('#locale');
  for (const language of [
    ['zh-TW', '請在 10 分鐘內於裝置上完成配對；權杖只會回傳一次', '複製指令', '--name "我的掌機"'],
    ['ja', '10 分以内に端末でペアリングしてください。トークンは一度だけ返されます。', 'コマンドをコピー', '--name "自分の携帯機"'],
    ['en', 'Complete pairing on the device within 10 minutes; the token is returned only once', 'Copy command', '--name "My handheld"'],
  ]) {
    await pairingLocale.selectOption(language[0]);
    await expect(page.locator('#pairing-code-result small')).toHaveText(language[1]);
    await expect(page.locator('#pairing-code-result button')).toHaveText(language[2]);
    await expect(page.locator('#pairing-code-result code')).toContainText(language[3]);
  }
  await pairingLocale.selectOption('zh-CN');
  await page.locator('#pair-device-profile').selectOption('builtin-device-android-handheld');
  await page.locator('#issue-pairing-code').click();
  await expect(page.locator('#pairing-code-result code')).toHaveText('http://127.0.0.1:18080');
  await expect(page.locator('#pairing-code-result code')).not.toContainText('varkiv agent pair');
  for (const [localeValue, instruction, copyLabel] of [
    ['zh-TW', '在 Android 裝置用戶端中填入服務位址與配對碼；權杖僅回傳一次', '複製配對碼'],
    ['ja', 'Android デバイスクライアントにサービス URL とペアリングコードを入力します。トークンは一度だけ返されます。', 'ペアリングコードをコピー'],
    ['en', 'Enter the service URL and pairing code in the Android device client; the token is returned only once', 'Copy pairing code'],
    ['zh-CN', '在 Android 设备客户端中填写服务地址与配对码；令牌仅返回一次', '复制配对码'],
  ]) {
    await pairingLocale.selectOption(localeValue);
    await expect(page.locator('#pairing-code-result small')).toHaveText(instruction);
    await expect(page.locator('#pairing-code-result button')).toHaveText(copyLabel);
  }
  await expect(page.locator('#sync-session-list')).toContainText('尚无同步会话');

  const manifestResponse = await page.request.get('/api/v1/sync/manifest');
  expect(manifestResponse.ok()).toBe(true);
  const manifest = await manifestResponse.json();
  expect(manifest.matching_order[0]).toBe('sha256');
  expect(manifest.editions).toHaveLength(3);
  expect(manifest.editions.every(edition => edition.save_namespace && edition.artifacts[0]?.sha256)).toBe(true);

  const streamsResponse = await page.request.get('/api/v1/save-streams');
  expect(streamsResponse.ok()).toBe(true);
  const stream = (await streamsResponse.json()).data.find(item => item.editions.length > 0);
  expect(stream).toBeTruthy();
  const editionID = stream.editions[0].edition_id;
  const deviceResponse = await page.request.post('/api/v1/devices', {
    data: {
      id: 'e2e-save-history-device',
      name: 'E2E save device',
      device_profile_id: 'builtin-device-windows-handheld',
      os_family: 'windows',
      distribution: 'windows',
      architecture: 'x86_64',
      capabilities: { save_sync: true }
    }
  });
  expect(deviceResponse.ok()).toBe(true);
  const device = await deviceResponse.json();
  const upload = await page.evaluate(async ({ streamID, editionID, deviceID }) => {
    const form = new FormData();
    form.append('manifest', JSON.stringify([
      { logical_path: 'card/Mcd001.ps2', mode: 384 },
      { logical_path: 'state/index.json', mode: 384 }
    ]));
    form.append('edition_id', editionID);
    form.append('device_id', deviceID);
    form.append('files', new Blob(['memory-card']), 'Mcd001.ps2');
    form.append('files', new Blob(['state-index']), 'index.json');
    const response = await fetch(`/api/v1/save-streams/${encodeURIComponent(streamID)}/revisions`, {
      method: 'POST',
      body: form
    });
    return { status: response.status, body: await response.json() };
  }, { streamID: stream.id, editionID, deviceID: device.id });
  expect(upload.status).toBe(201);
  expect(upload.body.revision.files).toHaveLength(2);

  await page.reload();
  await expect(page.locator('#sync-coverage')).not.toContainText('builtin-driver-');
  await expect(page.locator('#save-history')).not.toContainText('builtin-driver-');
  const archiveButton = page.locator(`[data-revision-archive="${upload.body.revision.id}"]`);
  await expect(archiveButton).toHaveText('下载完整快照');
  const archiveResponse = await page.request.get(`/api/v1/save-revisions/${upload.body.revision.id}/archive`);
  expect(archiveResponse.ok()).toBe(true);
  expect(archiveResponse.headers()['content-type']).toBe('application/zip');
  expect((await archiveResponse.body()).subarray(0, 2).toString()).toBe('PK');

  const locale = page.locator('#locale');
  await locale.selectOption('zh-TW');
  await expect(page.locator('#devices-view h1')).toHaveText('存檔同步');
  await expect(archiveButton).toHaveText('下載完整快照');
  await expect(page.locator('#pair-device-profile option').first()).toHaveText('Android 掌機');
  await locale.selectOption('ja');
  await expect(page.locator('#devices-view h1')).toHaveText('セーブ同期');
  await expect(archiveButton).toHaveText('完全なスナップショットをダウンロード');
  await expect(page.locator('#pair-device-profile option').first()).toHaveText('Android 携帯機');
  await locale.selectOption('en');
  await expect(page.locator('#devices-view h1')).toHaveText('Save sync');
  await expect(archiveButton).toHaveText('Download full snapshot');
  await expect(page.locator('#pair-device-profile option').first()).toHaveText('Android handheld');

  await page.setViewportSize({ width: 390, height: 844 });
  await expect.poll(() => page.evaluate(() => [...document.querySelectorAll('body *:not(.ambient)')]
    .map(element => {
      const rect = element.getBoundingClientRect();
      return { tag: element.tagName, className: element.className, right: Math.round(rect.right), width: Math.round(rect.width), text: element.textContent?.trim().slice(0, 80) };
    })
    .filter(item => item.right > window.innerWidth + 1 && item.width > 0)
    .slice(0, 12))).toEqual([]);
});

test('ambiguous device ROM matching is previewed and confirmed without exposing local identity', async ({ page }) => {
  const createGame = async (defaultTitle, localizedTitle) => {
    const gameResponse = await page.request.post('/api/v1/games', { data: { default_title: defaultTitle, platform: 'gba', titles: { 'zh-CN': localizedTitle } } });
    expect(gameResponse.ok()).toBe(true);
    const game = await gameResponse.json();
    const editionResponse = await page.request.post('/api/v1/editions', {
      data: { game_id: game.id, default_title: defaultTitle, edition_type: defaultTitle.includes('Translation') ? 'translation' : 'original', serial: 'E2E-AMBIGUOUS-ROM' },
    });
    expect(editionResponse.ok()).toBe(true);
    const updated = await editionResponse.json();
    return updated.editions.find(edition => edition.serial === 'E2E-AMBIGUOUS-ROM');
  };
  const original = await createGame('E2E Match Original', '端到端匹配原版');
  const translation = await createGame('E2E Match Translation', '端到端匹配汉化版');
  expect(original.id).not.toBe(translation.id);
  const deviceResponse = await page.request.post('/api/v1/devices', { data: { id: 'e2e-inventory-review-device', name: 'Review handheld', os_family: 'linux', architecture: 'arm64' } });
  expect(deviceResponse.ok()).toBe(true);
  const device = await deviceResponse.json();
  const opaqueClientItem = 'f'.repeat(64);
  const syncResponse = await page.request.post('/api/v1/sync/sessions', {
    headers: { 'Idempotency-Key': 'e2e-inventory-review' },
    data: { device_id: device.id, inventory: [{ client_item_id: opaqueClientItem, platform_id: 'gba', serial: 'E2E-AMBIGUOUS-ROM', size: 8388608 }], saves: [] },
  });
  expect(syncResponse.status()).toBe(201);
  const sync = await syncResponse.json();
  expect(sync.inventory[0]).toMatchObject({ match_status: 'ambiguous', match_method: 'serial' });

  await page.goto('/?e2e=inventory-review#sync');
  const panel = page.locator('.inventory-match-panel');
  await expect(panel).toBeVisible();
  await expect(panel.locator('.inventory-match-card')).toHaveCount(1);
  await expect(panel.locator('.inventory-candidate')).toHaveCount(2);
  await expect(panel).not.toContainText('E2E-AMBIGUOUS-ROM');
  await expect(panel).not.toContainText(opaqueClientItem);
  await expect(panel).not.toContainText(/\.gba|\/library\//);

  await page.locator('#locale').selectOption('zh-TW');
  await expect(panel.locator('h2')).toHaveText('待確認版本對應');
  await page.locator('#locale').selectOption('ja');
  await expect(panel.locator('h2')).toHaveText('確認待ちのエディション対応');
  await page.locator('#locale').selectOption('en');
  await expect(panel.locator('h2')).toHaveText('Edition mappings requiring review');
  await expect(panel).toContainText('Shown only when a device identifier matches multiple editions. The server does not display ROM filenames, paths, or hashes.');
  await expect(panel).not.toContainText(/[\u3400-\u9fff]/);
  await page.locator('#locale').selectOption('zh-CN');
  const choice = panel.locator('.inventory-candidate', { hasText: '端到端匹配汉化版' });
  await choice.click();
  await expect(choice).toHaveClass(/selected/);
  await panel.locator('[data-inventory-preview]').click();
  await expect(panel.locator('.match-confirmation')).toContainText('端到端匹配汉化版');
  await expect(panel.locator('.match-confirmation')).toContainText('下一次同步开始生效');

  await page.setViewportSize({ width: 390, height: 844 });
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBe(0);
  const mobileCandidateColumns = await panel.locator('.inventory-candidates').evaluate(element => getComputedStyle(element).gridTemplateColumns.split(' ').length);
  expect(mobileCandidateColumns).toBe(1);
  await panel.locator('[data-inventory-commit]').click();
  await expect(panel.locator('.inventory-match-card')).toHaveCount(0);
  await expect(panel).toContainText('没有需要确认的 ROM');
});

test('real-device support readiness is localized, read-only, and responsive', async ({ page }) => {
  const response = await page.request.get('/api/v1/support-readiness');
  expect(response.ok()).toBe(true);
  const report = await response.json();
  expect(report.format).toBe('varkiv-hardware-readiness-v1');
  expect(report.ready).toBe(false);
  expect(report.gates).toHaveLength(4);
  expect(report.gates.every(gate => gate.status === 'pending' && gate.missing.length > 0)).toBe(true);
  expect(JSON.stringify(report)).not.toContain('/' + 'Users/');
  expect(JSON.stringify(report)).not.toContain('token');

  const rejectedWrite = await page.request.post('/api/v1/support-readiness', { data: {} });
  expect(rejectedWrite.status()).toBe(405);
  expect(rejectedWrite.headers().allow).toBe('GET');

  await page.goto('/?e2e=support-readiness#settings');
  await expect(page.locator('#support-readiness-grid .support-gate')).toHaveCount(4);
  await expect(page.locator('#support-readiness-grid .support-gate > header > span svg')).toHaveCount(4);
  await expect.poll(() => page.locator('#support-readiness-grid .support-gate > header > span').evaluateAll(marks => marks.every(mark => mark.textContent.trim() === ''))).toBe(true);
  await expect(page.locator('#support-readiness-state')).toHaveText('仍有真机验证待完成');
  await expect(page.locator('#support-readiness-grid')).toContainText('Windows + RetroArch 自动同步');
  await expect(page.locator('#support-readiness-grid')).toContainText('PPSSPP 驱动');
  const readinessDisclosure = page.locator('#support-readiness .context-disclosure');
  await expect(readinessDisclosure).not.toHaveAttribute('open', '');
  await expect(readinessDisclosure.locator('p')).toBeHidden();
  await readinessDisclosure.locator('summary').click();
  await expect(readinessDisclosure).toHaveAttribute('open', '');
  await expect(readinessDisclosure.locator('p')).toBeVisible();
  await readinessDisclosure.locator('summary').click();

  await page.locator('#locale').selectOption('en');
  await expect(page.locator('#support-readiness-title')).toHaveText('Device verification matrix');
  await expect(page.locator('#support-readiness-state')).toHaveText('Real-device verification is still pending');
  await expect(readinessDisclosure.locator('summary')).toHaveText('Evidence & privacy');
  await expect(page.locator('#support-readiness')).not.toContainText(/[\u3400-\u9fff]/);

  await page.setViewportSize({ width: 390, height: 844 });
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  const cardXs = await page.locator('#support-readiness-grid .support-gate').evaluateAll(cards => cards.map(card => Math.round(card.getBoundingClientRect().x)));
  expect(new Set(cardXs).size).toBe(1);
});

test('privacy-minimized hardware report is reviewed, localized, and committed atomically', async ({ page }) => {
  const enabled = true;
  const create = async (endpoint, data) => {
    const response = await page.request.post(`/api/v1/${endpoint}`, { data });
    expect(response.ok(), `${endpoint}: ${await response.text()}`).toBe(true);
    return response.json();
  };
  const frontend = await create('frontend-adapters', {
    id: 'e2e-acceptance-frontend', name: 'E2E Frontend', format: 'e2e-acceptance', contract_version: 1,
    capabilities: { export: true }, support_level: 'catalogued', evidence: { scope: 'user' }, enabled
  });
  const driver = await create('emulator-drivers', {
    id: 'e2e-acceptance-driver', name: 'E2E Emulator', family: 'retroarch', contract_version: 1,
    platforms: ['gba'], targets: ['e2e-acceptance'], launch: { requires_core: true, arguments: ['{{rom.path}}'] },
    save: { scope: 'game', layout: 'single-file', patterns: [], refresh: 'process-exit', portability: 'same-driver' },
    config_paths: {}, support_level: 'catalogued', evidence: { scope: 'user' }, enabled
  });
  const standalone = await create('emulator-drivers', {
    id: 'e2e-acceptance-standalone', name: 'E2E Standalone', family: 'standalone', contract_version: 1,
    platforms: ['gba'], targets: ['e2e-acceptance'], launch: { requires_core: false, arguments: ['{{rom.path}}'] },
    save: { scope: 'game', layout: 'directory', patterns: [], refresh: 'process-exit', portability: 'same-driver' },
    config_paths: {}, support_level: 'catalogued', evidence: { scope: 'user' }, enabled
  });
  const core = await create('retroarch-cores', {
    id: 'e2e-acceptance-core', name: 'E2E Core', contract_version: 1,
    library_names: ['e2e_libretro'], platforms: ['gba'], support_level: 'catalogued', evidence: { scope: 'user' }, enabled
  });
  const device = await create('device-profiles', {
    id: 'e2e-acceptance-device', name: 'E2E Handheld', contract_version: 1, target: 'e2e-acceptance',
    os_family: 'windows', distribution: 'windows', architecture: 'x86_64', path_style: 'windows', max_path: 260,
    case_sensitive: false, supports_hardlink: true, supports_hooks: true, default_frontend_id: frontend.id,
    paths: { rom_dir: 'roms', save_dir: 'saves', config_dir: 'config', emulator_dir: 'emulators', core_dir: 'cores' },
    support_level: 'catalogued', evidence: { scope: 'user' }, enabled
  });
  const now = new Date(Date.now() - 60_000).toISOString();
  const report = {
    format: 'varkiv-hardware-acceptance-v1', generated_at: now, agent_version: 'e2e-24',
    host_os: 'windows', host_architecture: 'amd64', target: 'e2e-acceptance', config_protected: true,
    roots: { agent_root_real: true, rom_roots_configured: 1, rom_roots_real: true, driver_roots_configured: 1, driver_roots_real: true, path_overrides: 0 },
    runtime: {
      target: 'e2e-acceptance', emulator_dir_configured: true, core_dir_configured: true,
      drivers: [{ id: driver.id, name: driver.name, status: 'installed', match: 'private-emulator.exe' }, { id: standalone.id, name: standalone.name, status: 'installed', match: 'private-standalone.exe' }],
      retroarch_cores: [{ id: core.id, name: core.name, status: 'installed', match: 'private-core.dll' }],
      installed_drivers: 2, installed_cores: 1
    },
    last_sync: { state: 'complete', attempted_at: now, finished_at: now, last_success_at: now, session_recorded: true, uploaded: 1, downloaded: 1, conflicts: 0 },
    observed_on_hardware: ['frontend-launch', 'rom-launch', 'emulator-exit', 'save-created', 'sync-upload', 'sync-download', 'conflict-recovery', 'offline-play', 'sleep-resume', 'token-revocation', 'upgrade'],
    software_preflight_passed: true, evidence_level: 'candidate', requires_maintainer_review: true, contains_private_data: false
  };

  await page.goto('/?e2e=hardware-acceptance#settings');
  await page.locator('#acceptance-file').setInputFiles({ name: 'acceptance.json', mimeType: 'application/json', buffer: Buffer.from(JSON.stringify(report)) });
  await expect(page.locator('#acceptance-device-profile')).toHaveValue(device.id);
  await page.locator('#acceptance-driver').selectOption(driver.id);
  await expect(page.locator('#acceptance-driver')).toHaveValue(driver.id);
  await expect(page.locator('#acceptance-core-field')).toBeVisible();
  await expect(page.locator('#acceptance-core')).toHaveValue(core.id);
  await page.locator('#preview-acceptance').click();
  await expect(page.locator('#acceptance-preview')).toBeVisible();
  await expect(page.locator('#acceptance-preview h3')).toHaveText('存档同步已验证');
  await expect(page.locator('#acceptance-preview')).not.toContainText('private-emulator.exe');
  await expect(page.locator('#acceptance-preview')).not.toContainText('private-standalone.exe');
  await expect(page.locator('#acceptance-preview')).not.toContainText('private-core.dll');

  await page.locator('#locale').selectOption('en');
  await expect(page.locator('#acceptance-title')).toHaveText('Real-device verification');
  await expect(page.locator('#acceptance-preview h3')).toHaveText('Save sync-tested');
  await expect(page.locator('#hardware-acceptance-review')).not.toContainText(/[\u3400-\u9fff]/);
  await page.locator('#locale').selectOption('zh-CN');
  await page.locator('#acceptance-confirm').check();
  await page.locator('#commit-acceptance').click();
  await expect(page.locator('.acceptance-complete')).toContainText('真机证据已记录');
  await expect(page.locator('.acceptance-complete')).toContainText('还有模拟器待核对');
  await expect(page.locator('#acceptance-file-name')).toHaveText('acceptance.json');
  await expect(page.locator('#acceptance-driver')).toHaveValue(standalone.id);
  await expect(page.locator('#acceptance-core-field')).toBeHidden();
  await expect(page.locator('#continue-acceptance')).toBeVisible();

  for (const [endpoint, id] of [['device-profiles', device.id], ['frontend-adapters', frontend.id], ['emulator-drivers', driver.id], ['retroarch-cores', core.id]]) {
    const response = await page.request.get(`/api/v1/${endpoint}/${id}`);
    expect(response.ok()).toBe(true);
    const item = await response.json();
    expect(item.support_level).toBe('sync-tested');
    expect(JSON.stringify(item.evidence)).not.toContain('private-emulator.exe');
    expect(JSON.stringify(item.evidence)).not.toContain('private-core.dll');
    expect(item.evidence.report_sha256).toMatch(/^[a-f0-9]{64}$/);
  }

  await page.locator('#locale').selectOption('en');
  await expect(page.locator('#continue-acceptance')).toHaveText('Continue review');
  await expect(page.locator('.acceptance-complete')).not.toContainText(/[\u3400-\u9fff]/);
  await page.locator('#locale').selectOption('zh-CN');
  await page.locator('#continue-acceptance').click();
  await expect(page.locator('#acceptance-preview')).toBeHidden();
  await expect(page.locator('#acceptance-core')).toHaveValue('');
  await page.locator('#preview-acceptance').click();
  await expect(page.locator('#acceptance-preview h3')).toHaveText('存档同步已验证');
  await page.locator('#acceptance-confirm').check();
  await page.locator('#commit-acceptance').click();
  await expect(page.locator('.acceptance-complete')).toContainText('已核对全部模拟器');
  await expect(page.locator('#continue-acceptance')).toHaveCount(0);
  const standaloneResponse = await page.request.get(`/api/v1/emulator-drivers/${standalone.id}`);
  expect(standaloneResponse.ok()).toBe(true);
  const standaloneItem = await standaloneResponse.json();
  expect(standaloneItem.support_level).toBe('sync-tested');
  expect(JSON.stringify(standaloneItem.evidence)).not.toContain('private-standalone.exe');

  await page.locator(`[data-runtime-kind="driver"][data-runtime-id="${driver.id}"]`).click();
  await expect(page.locator('#runtime-evidence-targets')).toBeVisible();
  await expect(page.locator('#runtime-evidence-targets')).toContainText('e2e-acceptance');
  await expect(page.locator('#runtime-evidence-targets article')).toHaveCount(1);

  await page.setViewportSize({ width: 390, height: 844 });
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);

  for (const [endpoint, id] of [['device-profiles', device.id], ['emulator-drivers', driver.id], ['emulator-drivers', standalone.id], ['retroarch-cores', core.id], ['frontend-adapters', frontend.id]]) {
    const response = await page.request.delete(`/api/v1/${endpoint}/${id}`);
    expect(response.ok()).toBe(true);
  }
});

test('managed storage cleanup is reviewable, localized, quarantined, and recoverable', async ({ page }) => {
  const stateRoot = path.resolve(__dirname, '../../.e2e-state');
  const relativePath = 'blobs/sha256/ee/e2e-private-orphan.png';
  const orphanPath = path.join(stateRoot, 'media', ...relativePath.split('/'));
  fs.mkdirSync(path.dirname(orphanPath), { recursive: true });
  fs.writeFileSync(orphanPath, 'owned e2e orphan');

  await page.goto('/?e2e=managed-cleanup#settings');
  await expect(page.locator('#managed-storage-maintenance')).toBeVisible();
  await page.locator('#preview-storage-cleanup').click();
  await expect(page.locator('#storage-cleanup-preview')).toBeVisible();
  const candidate = page.locator('.storage-cleanup-item', { hasText: relativePath });
  await expect(candidate).toHaveCount(1);
  await page.locator('#storage-cleanup-preview input[data-cleanup-id]').evaluateAll(inputs => inputs.forEach(input => { input.checked = false; }));
  await candidate.locator('input').check();

  await page.locator('#locale').selectOption('en');
  await expect(page.locator('#storage-maintenance-title')).toHaveText('Managed storage maintenance');
  await expect(page.locator('#managed-storage-maintenance')).not.toContainText(/[\u3400-\u9fff]/);
  await page.locator('#locale').selectOption('zh-CN');
  await page.locator('#confirm-storage-cleanup').check();
  await page.locator('#commit-storage-cleanup').click();
  await expect(page.locator('.storage-recovery-row').first()).toContainText('可恢复');
  expect(fs.existsSync(orphanPath)).toBe(false);

  const historyResponse = await page.request.get('/api/v1/storage-cleanup/runs');
  expect(historyResponse.ok()).toBe(true);
  const historyText = await historyResponse.text();
  expect(historyText).not.toContain('e2e-private-orphan.png');
  expect(historyText).not.toContain(stateRoot);

  page.once('dialog', dialog => dialog.accept());
  await page.locator('.storage-recovery-row').first().locator('button').click();
  await expect(page.locator('.storage-recovery-row').first()).toContainText('已恢复');
  expect(fs.readFileSync(orphanPath, 'utf8')).toBe('owned e2e orphan');

  await page.setViewportSize({ width: 390, height: 844 });
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  fs.rmSync(orphanPath);
});

test('import workflow has no horizontal overflow at handheld width', async ({ page }) => {
  // This deliberately traverses every primary route across desktop, handheld,
  // adaptive, and localized layouts (more than 100 fresh navigations). Shared
  // hosted runners can take well over the generic 30-second test budget; keep
  // the coverage intact and bound this exhaustive sweep explicitly.
  test.setTimeout(300_000);
  const geometryGameResponse = await page.request.post('/api/v1/games', {
    data: { default_title: 'Geometry Fixture', platform: 'gba', titles: {} },
  });
  expect(geometryGameResponse.ok()).toBe(true);
  const geometryGameID = (await geometryGameResponse.json()).id;

  await page.setViewportSize({ width: 390, height: 844 });
  await openTransfer(page);
  await expect(page.locator('.import-layout')).toBeVisible();
  await expect.poll(() => page.evaluate(() => window.scrollY)).toBe(0);
  await expect(page.locator('.sidebar')).toBeInViewport();
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await page.evaluate(() => window.scrollTo(0, 500));
  await expect.poll(() => page.evaluate(() => {
    const sidebar = document.querySelector('.sidebar').getBoundingClientRect();
    const topbar = document.querySelector('.topbar').getBoundingClientRect();
    return getComputedStyle(document.querySelector('.sidebar')).position === 'fixed'
      && Math.abs(sidebar.bottom - window.innerHeight) <= 1
      && Math.abs(topbar.top) <= 1;
  })).toBe(true);
  await page.evaluate(() => window.scrollTo(0, 0));
  const columns = await page.locator('.import-layout').evaluate(element => getComputedStyle(element).gridTemplateColumns.split(' ').length);
  expect(columns).toBe(1);
  await expect(page.locator('.import-kind-choice')).toHaveCSS('grid-template-columns', /1fr|354px|minmax\(0px, 1fr\)/);
  const kindOptionBoxes = await page.locator('.import-kind-option').evaluateAll(options => options.map(option => option.getBoundingClientRect().x));
  expect(new Set(kindOptionBoxes.map(value => Math.round(value))).size).toBe(1);
  const rememberedFieldBoxes = await page.locator('.remember-source>label').evaluateAll(labels => labels.map(label => label.getBoundingClientRect().x));
  expect(new Set(rememberedFieldBoxes.map(value => Math.round(value))).size).toBe(1);
  const rememberedCheckbox = await page.locator('[name="remember_source"]').boundingBox();
  expect(rememberedCheckbox?.width).toBeLessThanOrEqual(20);
  expect(rememberedCheckbox?.height).toBeLessThanOrEqual(20);
  if (await page.locator('.saved-source').count()) {
    const sourceScanBox = await page.locator('.saved-source .source-scan').first().boundingBox();
    const sourceToggleBox = await page.locator('.saved-source .source-toggle').first().boundingBox();
    if (sourceScanBox && sourceToggleBox) expect(Math.abs(sourceScanBox.y - sourceToggleBox.y)).toBeLessThan(2);
  }
  await page.goto('/?e2e=mobile-library#library');
  await expect(page.locator('[data-library-mode="list"]')).toBeVisible();
  await expect(page.locator('#recheck')).toBeVisible();
  await expect.poll(() => page.evaluate(() => window.scrollY)).toBe(0);
  await expect(page.locator('.sidebar')).toBeInViewport();
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  for (const hash of ['packages', 'sync', 'settings']) {
    await page.goto(`/?e2e=mobile-${hash}#${hash}`);
    await expect(page.locator('.view:visible')).toHaveCount(1);
    await expect.poll(() => page.evaluate(() => window.scrollY)).toBe(0);
    const localeBox = await page.locator('.locale-label').boundingBox();
    expect(localeBox.x + localeBox.width).toBeGreaterThanOrEqual(370);
    await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  }

  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto('/?e2e=desktop-library#library');
  await expect(page.locator('.management-actions').first()).toBeVisible();
  const rightmostAction = await page.locator('.management-actions').first().boundingBox();
  expect(rightmostAction).not.toBeNull();
  expect(rightmostAction.x + rightmostAction.width).toBeLessThanOrEqual(1440);
  await expect(page.locator('.management-actions').first().locator('button')).toHaveCount(2);

  for (const width of [390, 639, 800, 1024, 1280, 1440]) {
    await page.setViewportSize({ width, height: 900 });
    for (const hash of ['library', 'platforms', 'sources', 'packages', 'sync', 'settings']) {
      await page.goto(`/?e2e=geometry-${width}-${hash}#${hash}`);
      await expect(page.locator('.view:visible')).toHaveCount(1);
      const geometry = await page.evaluate(() => {
        const visible = element => {
          const style = getComputedStyle(element);
          const rect = element.getBoundingClientRect();
          return style.display !== 'none' && style.visibility !== 'hidden' && style.opacity !== '0' && rect.width > 0 && rect.height > 0;
        };
        const outside = [...document.querySelectorAll('button, input, select, textarea, a')]
          .filter(visible)
          .map(element => {
            const rect = element.getBoundingClientRect();
            return {
              tag: element.tagName,
              id: element.id,
              text: (element.textContent || element.getAttribute('aria-label') || '').trim().slice(0, 50),
              left: Math.round(rect.left),
              right: Math.round(rect.right),
            };
          })
          .filter(rect => rect.left < -1 || rect.right > window.innerWidth + 1);
        const undersizedButtons = window.innerWidth === 390
          ? [...document.querySelectorAll('button')]
            .filter(visible)
            .map(element => {
              const rect = element.getBoundingClientRect();
              return {
                className: element.className,
                text: (element.textContent || element.getAttribute('aria-label') || '').trim().slice(0, 50),
                height: Math.round(rect.height),
              };
            })
            .filter(rect => rect.height < 32)
          : [];
        return {
          outside,
          overflow: document.documentElement.scrollWidth - document.documentElement.clientWidth,
          undersizedButtons,
        };
      });
      expect(geometry, `${hash} geometry at ${width}px`).toEqual({ outside: [], overflow: 0, undersizedButtons: [] });

      if (width === 390) {
        await expect(page.locator('.brand-mark')).toBeHidden();
        const navGeometry = await page.locator('.sidebar').evaluate(element => {
          const rect = element.getBoundingClientRect();
          const labels = [...element.querySelectorAll('.nav-label')].map(label => {
            const labelRect = label.getBoundingClientRect();
            return { text: label.textContent.trim(), width: labelRect.width, height: labelRect.height };
          });
          return { position: getComputedStyle(element).position, bottom: rect.bottom, viewport: innerHeight, labels };
        });
        expect(navGeometry.position).toBe('fixed');
        expect(Math.abs(navGeometry.bottom - navGeometry.viewport)).toBeLessThanOrEqual(1);
        expect(navGeometry.labels).toHaveLength(6);
        expect(navGeometry.labels.every(label => label.text && label.width > 1 && label.height >= 10)).toBe(true);
        const topActionsBox = await page.locator('.top-actions').boundingBox();
        expect(topActionsBox.x + topActionsBox.width).toBeLessThanOrEqual(width - 18 + 1);
      }

      if ([800, 1024].includes(width) && hash === 'library') {
        const tableBox = await page.locator('.management-table').boundingBox();
        const actionBox = await page.locator('.management-actions').first().boundingBox();
        expect(actionBox.x + actionBox.width).toBeLessThanOrEqual(tableBox.x + tableBox.width + 1);
        await expect(page.locator('.management-actions').first().locator('button')).toHaveCount(2);
        const statsFit = await page.locator('.stats').evaluate(element => element.scrollWidth <= element.clientWidth + 1);
        expect(statsFit).toBe(true);
      }
    }
  }

  for (const viewport of [
    { name: 'phone', width: 390, height: 844 },
    { name: 'tablet', width: 768, height: 1024 },
    { name: 'landscape', width: 844, height: 390 },
  ]) {
    await page.setViewportSize(viewport);
    for (const hash of ['library', 'platforms', 'sources', 'packages', 'sync', 'settings']) {
      await page.goto(`/?e2e=adaptive-${viewport.name}-${hash}#${hash}`);
      await expect(page.locator('.view:visible')).toHaveCount(1);
      await page.evaluate(() => window.scrollTo(0, 0));
      const responsiveGeometry = await page.evaluate(() => {
        const selectors = ['.view:not([hidden])', '.sync-panel', '.settings-overview article', '.support-gate'];
        const overflowingContainers = [...document.querySelectorAll(selectors.join(','))]
          .filter(element => element.offsetParent)
          .filter(element => !['hidden', 'clip'].includes(getComputedStyle(element).overflowX))
          .map(element => ({
            selector: element.id || element.className,
            overflow: element.scrollWidth - element.clientWidth,
          }))
          .filter(element => element.overflow > 1);
        const sidebar = document.querySelector('.sidebar').getBoundingClientRect();
        const labels = [...document.querySelectorAll('.sidebar .nav-label')].map(label => label.getBoundingClientRect());
        const undersizedTargets = [...document.querySelectorAll('.view:not([hidden]) button, .view:not([hidden]) summary')]
          .map(element => {
            const style = getComputedStyle(element);
            const rect = element.getBoundingClientRect();
            return {
              text: (element.textContent || element.getAttribute('aria-label') || '').trim().slice(0, 40),
              width: Math.round(rect.width),
              height: Math.round(rect.height),
              visible: style.display !== 'none' && style.visibility !== 'hidden' && rect.width > 0 && rect.height > 0,
            };
          })
          .filter(target => target.visible && (target.width < 44 || target.height < 44));
        return {
          documentOverflow: document.documentElement.scrollWidth - document.documentElement.clientWidth,
          overflowingContainers,
          sidebar: { top: sidebar.top, bottom: sidebar.bottom, height: sidebar.height, position: getComputedStyle(document.querySelector('.sidebar')).position },
          labelsVisible: labels.every(rect => rect.width > 1 && rect.height >= 9),
          undersizedTargets,
        };
      });
      expect(responsiveGeometry.documentOverflow, `${viewport.name} ${hash} document overflow`).toBe(0);
      expect(responsiveGeometry.overflowingContainers, `${viewport.name} ${hash} container overflow`).toEqual([]);
      expect(responsiveGeometry.labelsVisible, `${viewport.name} ${hash} navigation labels`).toBe(true);
      expect(responsiveGeometry.undersizedTargets, `${viewport.name} ${hash} touch targets`).toEqual([]);
      if (viewport.name === 'phone') {
        expect(responsiveGeometry.sidebar.position).toBe('fixed');
        expect(Math.abs(responsiveGeometry.sidebar.bottom - viewport.height)).toBeLessThanOrEqual(1);
      } else {
        expect(responsiveGeometry.sidebar.position).toBe('sticky');
        expect(responsiveGeometry.sidebar.top).toBeGreaterThanOrEqual(0);
      }
    }
  }

  for (const width of [390, 1280]) {
    await page.setViewportSize({ width, height: 900 });
    for (const locale of ['zh-CN', 'zh-TW', 'ja', 'en']) {
      await page.goto('/?e2e=localized-geometry#library');
      await page.locator('#locale').selectOption(locale);
      for (const hash of ['library', 'platforms', 'sources', 'packages', 'sync', 'settings']) {
      await page.goto(`/?e2e=localized-geometry-${locale}-${hash}#${hash}`);
      await expect(page.locator('.view:visible')).toHaveCount(1);
      const localizedGeometry = await page.evaluate(() => ({
        overflow: document.documentElement.scrollWidth - document.documentElement.clientWidth,
        outsideLocalizedControls: [...document.querySelectorAll('#platforms-view .platform-filters button, #settings-view .runtime-catalog li, #settings-view .runtime-catalog li>button, #settings-view .runtime-item-state, #settings-view .support-level')]
          .filter(element => element.offsetParent)
          .map(element => {
            const rect = element.getBoundingClientRect();
            return {
              tag: element.tagName,
              className: element.className,
              text: element.textContent.trim().slice(0, 50),
              left: Math.round(rect.left),
              right: Math.round(rect.right),
            };
          })
          .filter(rect => rect.left < -1 || rect.right > window.innerWidth + 1),
        wrappingButtons: [...document.querySelectorAll('button')]
          .filter(element => element.offsetParent && element.textContent.trim())
          .filter(element => getComputedStyle(element).whiteSpace !== 'nowrap')
          .map(element => element.textContent.trim().slice(0, 50)),
        clippedButtons: [...document.querySelectorAll('button')]
          .filter(element => element.offsetParent && element.textContent.trim())
          .filter(element => element.scrollWidth > element.clientWidth + 1)
          .map(element => element.textContent.trim().slice(0, 50)),
        wrappedControlText: [...document.querySelectorAll('.nav-item, .profile-option, .platform-row-actions button')]
          .filter(element => element.offsetParent)
          .flatMap(element => {
            const nodes = [];
            const stack = [element];
            while (stack.length) {
              const node = stack.pop();
              if (node.nodeType === Node.TEXT_NODE && node.textContent.trim()) nodes.push(node);
              else if (node.childNodes) stack.push(...node.childNodes);
            }
            return nodes.flatMap(node => {
              const parentRect = node.parentElement.getBoundingClientRect();
              if (parentRect.width <= 1 || parentRect.height <= 1) return [];
              const value = node.textContent;
              const start = value.search(/\S/);
              const end = value.length - (value.match(/\s*$/)?.[0].length || 0);
              const range = document.createRange();
              range.setStart(node, start);
              range.setEnd(node, end);
              const lineYs = [...range.getClientRects()]
                .filter(rect => rect.width > 0 && rect.height > 0)
                .reduce((values, rect) => values.some(value => Math.abs(value - rect.y) <= 2) ? values : [...values, rect.y], []);
              return lineYs.length > 1 ? [{ control: element.className, text: value.trim().slice(0, 50), lines: lineYs.length }] : [];
            });
          }),
      }));
      expect(localizedGeometry, `${locale} ${hash} localized geometry at ${width}px`).toEqual({
        overflow: 0,
        outsideLocalizedControls: [],
        wrappingButtons: [],
        clippedButtons: [],
        wrappedControlText: [],
      });
      }
    }
  }

  expect((await page.request.delete(`/api/v1/games/${geometryGameID}`)).ok()).toBe(true);
});
