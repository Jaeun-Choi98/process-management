import React, { useState } from 'react';
import Icon from '../components/Icon/Icon';
import { login } from '../api/auth';
import './Login.css';

interface LoginProps {
  onLogin: () => void;
}

/**
 * 로그인 화면. 계정은 Go 서버 소스(internal/config/config.go)에 있다.
 * 새 탭 · 새 브라우저 창으로 들어오면 토큰이 없어 이 화면부터 보인다.
 */
const Login: React.FC<LoginProps> = ({ onLogin }) => {
  const [user, setUser] = useState('');
  const [password, setPassword] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setSubmitting(true);
    setError(null);
    try {
      await login(user.trim(), password);
      onLogin();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
      setPassword('');
      setSubmitting(false);
    }
  };

  return (
    <main className="login">
      <form className="login-form" onSubmit={submit}>
        <h1 className="login-title">
          <Icon name="activity" />
          Process Management
        </h1>
        <label className="login-field">
          <span>아이디 <em aria-hidden="true">*</em></span>
          <input
            className="input"
            name="username"
            autoComplete="username"
            value={user}
            onChange={e => setUser(e.target.value)}
            required
            autoFocus
          />
        </label>
        <label className="login-field">
          <span>비밀번호 <em aria-hidden="true">*</em></span>
          <input
            className="input"
            type="password"
            name="password"
            autoComplete="current-password"
            value={password}
            onChange={e => setPassword(e.target.value)}
            required
          />
        </label>
        {error && <div className="login-error" role="alert">{error}</div>}
        <button className="btn btn-primary" type="submit" disabled={submitting || !user.trim() || !password}>
          {submitting ? '확인 중...' : '로그인'}
        </button>
      </form>
    </main>
  );
};

export default Login;
