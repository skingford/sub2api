// This script replaces only the JavaScript entrypoint in a local lab copy.
// The original native HTTP/checksum machine code handles these requests.
async function run() {
  const cases = await Bun.file('/work/cases.json').json();
  for (const test of cases) {
    const response = await fetch('https://api.anthropic.com' + (test.path || '/v1/messages'), {
      method: 'POST',
      headers: {
        'content-type': 'application/json',
        'x-api-key': 'local-container-key-not-a-real-credential',
        ...(test.version === false ? {} : { 'anthropic-version': '2023-06-01' }),
        'x-lab-case': test.name,
      },
      body: test.body,
    });
    await response.text();
  }
  console.log(JSON.stringify({ count: cases.length, runtime: Bun.version }));
}
run().catch(error => { console.error(error); process.exitCode = 1; });
