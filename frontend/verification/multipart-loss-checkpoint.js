async (page) => {
  // Run after selecting loss-65.bin, with transport index 2 blocked.
  // This only reads the authenticated fixture and checks the handoff barrier.
  const probe = page.__acervoRecovery;
  if (!probe || !probe.blockPartIndexes.includes(2)) {
    throw new Error('Install recovery transport and block part 2 first');
  }
  const response = await page.request.get('http://127.0.0.1:28100/api/uploads');
  if (response.status() !== 200) throw new Error(`Fixture lookup HTTP ${response.status()}`);
  const { operations } = await response.json();
  const candidates = operations.filter((op) => op.name === 'loss-65.bin');
  if (candidates.length !== 1) throw new Error('Expected exactly one loss-65.bin operation');
  const op = candidates[0];
  if (op.size !== 68157440 || op.status !== 'pending' || op.parts.length !== 3) {
    throw new Error('Fixture must be pending, 65 MiB, three parts');
  }
  const expected = [33554432, 33554432, 1048576];
  for (let index = 0; index < expected.length; index++) {
    const part = op.parts[index];
    if (part.index !== index || part.size !== expected[index] || part.offset !== index * 33554432) {
      throw new Error(`Unexpected manifest for part ${index}`);
    }
    const availability = index < 2 ? 'available' : 'missing';
    if (part.availability !== availability || part.available !== (index < 2)) {
      throw new Error(`Part ${index} is ${part.availability}, expected ${availability}`);
    }
    const path = `/api/uploads/${op.id}/parts/${index}`;
    const accepted = probe.events.some((event) => event.method === 'PUT' && event.path === path && event.status === 204);
    if (accepted !== (index < 2)) throw new Error(`Unexpected PUT acknowledgement for part ${index}`);
  }
  const checkpoint = {
    at: new Date().toISOString(),
    action: 'ready-for-real-coordinator-and-part-loss',
    operation: op,
    events: probe.events,
  };
  // No cookies, headers or credentials in this return value.
  return checkpoint;
}
