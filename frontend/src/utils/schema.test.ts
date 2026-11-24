import { checkFilePattern, conditionFromCommand, effectivePatterns, migrateLegacyMatch, scriptPath, validateProcessConfig } from './schema';

const valid = {
  pollIntervalMs: 5000,
  groups: [{ slug: 'uw', name: '의왕' }],
  servers: [{ id: 'uw-app1', group: 'uw', host: '10.0.0.11', port: 22, user: 'ops', hasPassword: true }],
  processes: [
    {
      id: 'shelter',
      server: 'uw-app1',
      matchName: 'java',
      matchArgs: '/opt/shelter/shelter.jar',
      logFiles: ['/opt/shelter/logs/*.log', '/opt/shelter/logs/*/*.log'],
      iniFiles: ['/opt/shelter/conf/*.ini'],
      startScript: '/opt/shelter/start.sh',
      autoRestart: true,
    },
  ],
};

test('accepts a valid config (saved password counts as password)', () => {
  expect(validateProcessConfig(valid)).toEqual({ ok: true, config: valid });
});

test('collects every error with its location', () => {
  const result = validateProcessConfig({
    pollIntervalMs: 10,
    watchIntervalMs: 500,
    restartDelayMs: 1.5,
    groups: [{ slug: 'uw', name: '의왕' }, { slug: 'all', name: 'x' }, { slug: 'uw', name: '' }],
    servers: [{ id: 'a', group: 'nope', host: 'bad host', port: 0, user: '' }],
    processes: [
      { id: 'p', server: 'zzz', matchName: '/usr/bin/java', matchRegex: '(', logFiles: ['/home/*'], startScript: 'start.sh; rm -rf /' },
      { id: 'q', server: 'zzz', autoRestart: true },
      // 조건 없이 등록만 한 프로세스는 허용한다.
      { id: 'r', server: 'a' },
    ],
  });
  expect(result.ok).toBe(false);
  const fields = result.ok ? [] : result.errors.map(error => error.split(':')[0]);
  expect(fields).toEqual([
    'pollIntervalMs',
    'watchIntervalMs',
    'restartDelayMs',
    'groups[1].slug',
    'groups[2].slug',
    'groups[2].name',
    'servers[0].group',
    'servers[0].host',
    'servers[0].port',
    'servers[0].user',
    'servers[0].password',
    'processes[0].server',
    'processes[0].matchRegex',
    'processes[0].matchRegex',
    'processes[0].matchName',
    'processes[0].startScript',
    'processes[0].logFiles',
    'processes[1].server',
    'processes[1].autoRestart',
    'processes[1].startScript',
  ]);
});

test('rejects a non-object root and missing arrays', () => {
  expect(validateProcessConfig([])).toEqual({ ok: false, errors: [expect.stringContaining('최상위')] });
  expect(validateProcessConfig({ groups: [] })).toEqual({ ok: false, errors: ['groups, servers, processes: 배열이 필요합니다'] });
});

test('file patterns follow the same rules as the Go server', () => {
  for (const ok of ['/opt/app/logs/*.log', '/opt/app/logs/*', '/opt/app/logs/*/*.log', '/opt/app.log', '/home/user/app-?.log']) {
    expect(checkFilePattern(ok, false)).toBe('');
  }
  for (const bad of ['/home/*', '/*/app.log', '/opt/*/x.log', '/opt/app/**', '/opt/../etc/x.log', 'opt/app/x.log', '/opt/app/x y.log', '/opt']) {
    expect(checkFilePattern(bad, false)).not.toBe('');
  }
  expect(checkFilePattern('/opt/app/conf/*.ini', true)).toBe('');
  expect(checkFilePattern('/opt/app/conf/*', true)).not.toBe('');
});

test('file patterns default to the start script folder and resolve relative paths', () => {
  expect(effectivePatterns({ startScript: '/opt/shelter/start.sh' }, 'log').patterns).toEqual([
    '/opt/shelter/*.log',
    '/opt/shelter/*/*.log',
    '/opt/shelter/*/*/*.log',
    '/opt/shelter/*/*/*/*.log',
    '/opt/shelter/*/*/*/*/*.log',
  ]);
  expect(effectivePatterns({ startScript: '/opt/shelter/start.sh', iniFiles: ['conf/*.ini', '/etc/shelter/*.ini'] }, 'ini').patterns).toEqual([
    '/opt/shelter/conf/*.ini',
    '/etc/shelter/*.ini',
  ]);
  expect(effectivePatterns({}, 'log').problem).toBeTruthy();
  expect(effectivePatterns({ startScript: '/opt/start.sh' }, 'log').problem).toBeTruthy();

  const config = (process: object) => ({
    groups: [{ slug: 'uw', name: '의왕' }],
    servers: [{ id: 's', group: 'uw', host: 'h', user: 'u', hasPassword: true }],
    processes: [{ id: 'p', server: 's', matchRegex: 'x', ...process }],
  });
  expect(validateProcessConfig(config({ startScript: '/opt/shelter/start.sh', logFiles: ['logs/*.log'] })).ok).toBe(true);
  expect(validateProcessConfig(config({ logFiles: ['logs/*.log'] })).ok).toBe(false);
  expect(validateProcessConfig(config({ startScript: '/opt/shelter/start.sh', logFiles: ['../etc/*.log'] })).ok).toBe(false);
});

test('old config files are converted to a single condition (same as the Go server)', () => {
  const migrated = migrateLegacyMatch({
    groups: [],
    servers: [],
    processes: [
      { id: 'a', server: 's', match: 'java .*a', autoRestart: true, watchName: 'java', watchArgs: 'a.jar' },
      { id: 'b', server: 's', match: 'java .*b' },
      { id: 'c', server: 's', matchArgs: 'c.jar' },
    ],
  });
  expect(migrated).toEqual({
    groups: [],
    servers: [],
    processes: [
      { id: 'a', server: 's', autoRestart: true, matchName: 'java', matchArgs: 'a.jar' },
      { id: 'b', server: 's', matchRegex: 'java .*b' },
      { id: 'c', server: 's', matchArgs: 'c.jar' },
    ],
  });
});

test('실행 중인 프로세스 명령줄로 조건을 만든다', () => {
  expect(conditionFromCommand('/usr/bin/java -Xmx1g -Dlog.dir=/var/log -jar /opt/app/app.jar --port 8080')).toEqual({ matchName: 'java', matchArgs: '/opt/app/app.jar' });
  expect(conditionFromCommand('python3 manage.py runserver')).toEqual({ matchName: 'python3', matchArgs: 'manage.py' });
  expect(conditionFromCommand('/home/ops/app/bin/server -c /home/ops/app/conf/a.conf')).toEqual({ matchName: 'server', matchArgs: '/home/ops/app/conf/a.conf' });
  expect(conditionFromCommand('nginx: master process /usr/sbin/nginx')).toEqual({ matchName: 'nginx:', matchArgs: '/usr/sbin/nginx' });
  expect(conditionFromCommand('/opt/app/daemon')).toEqual({ matchName: 'daemon', matchArgs: '' });
});

test('시작 스크립트는 파일 이름만 적으면 서버 기본 폴더 기준이다', () => {
  expect(scriptPath('bin/start.sh', '/home/ops/app')).toBe('/home/ops/app/bin/start.sh');
  expect(scriptPath('/opt/a/start.sh', '/home/ops/app')).toBe('/opt/a/start.sh');
  const config = (baseDir: string | undefined, startScript: string) => ({
    groups: [{ slug: 'uw', name: '의왕' }],
    servers: [{ id: 's1', group: 'uw', host: '10.0.0.1', user: 'ops', password: 'pw', baseDir }],
    processes: [{ id: 'p1', server: 's1', startScript, logFiles: ['logs/*.log'] }],
  });
  expect(validateProcessConfig(config('/home/ops/app', 'start.sh')).ok).toBe(true);
  expect(validateProcessConfig(config(undefined, '/opt/app/start.sh')).ok).toBe(true);
  const errors = (c: unknown) => {
    const result = validateProcessConfig(c);
    return result.ok ? [] : result.errors;
  };
  expect(errors(config(undefined, 'start.sh')).join()).toContain('기본 폴더가 필요합니다');
  expect(errors(config('/home/ops/app', '../start.sh')).join()).toContain('startScript');
  expect(errors(config('home/ops', '/opt/a/start.sh')).join()).toContain('baseDir');
});
