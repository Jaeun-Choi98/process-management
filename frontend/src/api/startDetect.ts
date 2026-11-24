import { requestApi } from './client';
import { ProcessConfig, ProcessInstance } from '../types/config';
import { conditionFromCommand } from '../utils/schema';

/**
 * 실행 판단 조건이 없는 프로세스를 처음 시작할 때, 시작 전후의 ps 를 비교해 새로 뜬 프로세스로 조건을 정한다.
 * (프로그램 설치 후 시작 · 조건 없는 프로세스의 [시작])
 */

const API = process.env.PUBLIC_URL;
/** 시작한 뒤 프로세스가 뜨기를 기다리는 시간: 처음 3초, 그 뒤 2초 간격으로 4번 더 (최대 약 11초) */
const FIRST_WAIT_MS = 3000;
const RETRY_WAIT_MS = 2000;
const RETRIES = 4;
/** 시작 스크립트를 감싸기만 하는 셸 (java 등 실제 프로그램이 따로 있으면 이것은 고르지 않는다) */
const WRAPPERS = new Set(['sh', 'bash', 'dash', 'ksh', 'zsh', 'nohup']);

const sleep = (ms: number) => new Promise(resolve => setTimeout(resolve, ms));

export const fetchRunning = async (serverId: string) =>
  (await requestApi<{ processes: ProcessInstance[] }>(`${API}/api/servers/${encodeURIComponent(serverId)}/ps`)).processes;

export interface StartDetection {
  /** 조건을 정한 프로세스 (못 정했으면 undefined) */
  picked?: ProcessInstance;
  /** 시작한 뒤 새로 뜬 프로세스 (못 정했을 때 고르게 보여준다) */
  candidates: ProcessInstance[];
}

/**
 * 시작한 뒤 새로 뜬 프로세스를 찾는다. before 는 시작하기 직전의 목록, folder 는 프로그램 폴더(명령줄에 들어 있으면 우선).
 * 새로 뜬 것 중 폴더가 들어 있는 것 → 셸이 아닌 것 순으로 좁혀 하나만 남으면 그것을 고른다.
 */
export async function detectStarted(serverId: string, before: ProcessInstance[], folder: string): Promise<StartDetection> {
  const known = new Set(before.map(p => p.pid));
  let candidates: ProcessInstance[] = [];
  for (let attempt = 0; attempt <= RETRIES; attempt++) {
    await sleep(attempt === 0 ? FIRST_WAIT_MS : RETRY_WAIT_MS);
    candidates = (await fetchRunning(serverId)).filter(p => !known.has(p.pid));
    let narrowed = candidates.filter(p => p.command.includes(folder));
    if (narrowed.length === 0) narrowed = candidates;
    const programs = narrowed.filter(p => !WRAPPERS.has(conditionFromCommand(p.command).matchName));
    if (programs.length > 0) narrowed = programs;
    if (narrowed.length === 1) return { picked: narrowed[0], candidates };
  }
  return { candidates };
}

/** 고른 프로세스의 명령줄로 실행 판단 조건을 저장한다. 서버가 돌려준 전체 설정을 돌려준다. */
export const saveConditionFrom = (processId: string, instance: ProcessInstance, autoRestart: boolean) =>
  requestApi<ProcessConfig>(`${API}/api/processes/${encodeURIComponent(processId)}/watch`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ autoRestart, ...conditionFromCommand(instance.command), matchRegex: '' }),
  });

/** 프로세스를 시작한다. (POST /api/processes/:id/start) */
export const startProcess = (processId: string) =>
  requestApi<{ message: string }>(`${API}/api/processes/${encodeURIComponent(processId)}/start`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: '{}',
  });
