import React, { useCallback, useEffect, useState } from 'react';
import Dashboard from './pages/Dashboard';
import Login from './pages/Login';
import { logout } from './api/auth';
import { AUTH_REQUIRED_EVENT, getToken } from './api/token';

/**
 * 로그인: 이 탭에 토큰이 없으면(새 탭 · 새 브라우저) 로그인 화면을 보여준다.
 * API 가 401 을 돌려주면(만료 · Go 서버 재시작) 다시 로그인 화면으로 간다. 주소(/uw 등)는 그대로 둔다.
 *
 * 경로 라우팅: /all 은 전체 그룹, /uw 처럼 그룹 slug 경로는 그 그룹만 보여준다. ( / 는 /all 로 이동 )
 * Go 서버는 확장자 없는 경로를 모두 index.html 로 돌려주므로 새로고침 · 주소 직접 입력도 된다.
 */
function App(): React.ReactElement {
  const [pathname, setPathname] = useState(window.location.pathname);
  const [loggedIn, setLoggedIn] = useState(() => getToken() !== '');

  useEffect(() => {
    const onAuthRequired = () => setLoggedIn(false);
    window.addEventListener(AUTH_REQUIRED_EVENT, onAuthRequired);
    return () => window.removeEventListener(AUTH_REQUIRED_EVENT, onAuthRequired);
  }, []);

  useEffect(() => {
    const onPopState = () => setPathname(window.location.pathname);
    window.addEventListener('popstate', onPopState);
    return () => window.removeEventListener('popstate', onPopState);
  }, []);

  const navigate = useCallback((to: string) => {
    if (to === window.location.pathname) return;
    window.history.pushState(null, '', to);
    setPathname(to);
    window.scrollTo(0, 0);
  }, []);

  const slug = decodeURIComponent(pathname.split('/')[1] ?? '') || 'all';
  useEffect(() => {
    if (pathname === '/') window.history.replaceState(null, '', '/all');
  }, [pathname]);

  return (
    <div className="App">
      {loggedIn
        ? <Dashboard slug={slug} navigate={navigate} onLogout={() => { void logout(); }} />
        : <Login onLogin={() => setLoggedIn(true)} />}
    </div>
  );
}

export default App;
