async (page) => {
  // Run only inside an authorized, named playwright-cli browser session.
  // This installs transport faults; it does not select files or create uploads.
  if (page.__acervoRecovery) throw new Error('Probe already installed');
  const probe = {
    loseNextCreatedResponse: true,
    blockPartIndexes: [0],
    events: [],
  };
  page.__acervoRecovery = probe;
  const record = (value) => probe.events.push({ at: new Date().toISOString(), ...value });
  page.on('response', async (response) => {
    const path = response.url().replace(/^https?:\/\/[^/]+/, '').split('?')[0];
    if (!path.startsWith('/api/')) return;
    const request = response.request();
    const event = { method: request.method(), path, status: response.status() };
    if (response.ok() && request.method() === 'GET' && /^\/api\/uploads(?:\/[^/]+)?$/.test(path)) {
      try {
        const body = await response.json();
        event.operations = (body.operations || [body]).map((op) => ({
          id: op.id, key: op.idempotency_key, status: op.status,
          parts: op.parts?.map((part) => ({ index: part.index, availability: part.availability })),
        }));
      } catch { event.decodeFailed = true; }
    }
    record(event);
  });
  await page.route('**/api/uploads', async (route) => {
    if (route.request().method() !== 'POST') return route.continue();
    const body = route.request().postDataJSON();
    record({ action: 'create-attempt', key: body.idempotency_key, name: body.name });
    if (!probe.loseNextCreatedResponse) return route.continue();
    probe.loseNextCreatedResponse = false;
    const response = await route.fetch();
    if (response.status() === 201) {
      const operation = await response.json();
      probe.loseNextCreatedResponse = false;
      record({ action: 'created-response-lost', status: 201, id: operation.id, key: body.idempotency_key });
      await response.dispose();
      return route.abort('connectionfailed');
    }
    probe.loseNextCreatedResponse = true;
    await route.fulfill({ response });
    await response.dispose();
  });
  await page.route('**/api/uploads/*/parts/*', async (route) => {
    const request = route.request();
    const path = request.url().replace(/^https?:\/\/[^/]+/, '').split('?')[0];
    const index = Number(path.split('/').at(-1));
    const blocked = request.method() === 'PUT' && probe.blockPartIndexes.includes(index);
    record({ action: 'part-request', method: request.method(), path, index, blocked });
    return blocked ? route.abort('connectionfailed') : route.continue();
  });
  console.log('Transport probe installed. No files selected. Part 0 blocked; one real 201 will be hidden.');
}
