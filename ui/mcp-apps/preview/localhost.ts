// Legacy bookmark: use the same sandboxed host as browser tests.
const params = new URLSearchParams(location.search);
params.set('scenario', params.get('fixture') === 'viz' ? 'viz' : 'metrics');
location.replace(`http://127.0.0.1:5194/?${params}`);
