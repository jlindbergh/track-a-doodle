import { useEffect, useState, type ReactNode } from 'react';
import { fetchMe, logout, setUnauthorizedHandler, type User } from '../api';
import { useConfig } from '../config';
import { formatError } from '../format';
import { SessionContext } from '../session';
import LoginForm from './LoginForm';
import RegisterForm from './RegisterForm';
import VerifyForm from './VerifyForm';

interface Props {
  children: ReactNode;
}

/**
 * Renders children for a signed-in user, and the sign-in, sign-up or
 * account confirmation screen otherwise. In development mode there is no
 * sign-in, so children are rendered as they are.
 */
export default function AuthGate({ children }: Props) {
  const config = useConfig();
  if (config.auth.mode === 'dev') return children;
  return <PasswordAuth>{children}</PasswordAuth>;
}

/**
 * The token of an account confirmation link, /verify#token=…. It is in the
 * fragment, so it is never sent to the server with the page request.
 */
function readVerifyToken(): string | null {
  if (window.location.pathname !== '/verify') return null;
  return new URLSearchParams(window.location.hash.slice(1)).get('token');
}

function PasswordAuth({ children }: Props) {
  // undefined while the session is being checked.
  const [user, setUser] = useState<User | null | undefined>(undefined);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [verifyToken, setVerifyToken] = useState(readVerifyToken);
  const [screen, setScreen] = useState<'login' | 'register'>('login');

  useEffect(() => {
    const controller = new AbortController();
    fetchMe(controller.signal)
      .then(setUser)
      .catch((e: unknown) => {
        if (!controller.signal.aborted) setLoadError(formatError(e));
      });
    return () => controller.abort();
  }, []);

  // A request refused for want of a session, e.g. after it expired, shows
  // the sign-in screen.
  useEffect(() => {
    setUnauthorizedHandler(() => setUser(null));
    return () => setUnauthorizedHandler(null);
  }, []);

  function handleVerified(u: User) {
    window.history.replaceState(null, '', '/');
    setVerifyToken(null);
    setUser(u);
  }

  function handleCancelVerify() {
    window.history.replaceState(null, '', '/');
    setVerifyToken(null);
  }

  async function signOut() {
    await logout();
    setScreen('login');
    setUser(null);
  }

  if (loadError) return <p className="error app-message">{loadError}</p>;
  if (user === undefined) return null;

  if (verifyToken) {
    return (
      <AuthScreen>
        <VerifyForm token={verifyToken} onVerified={handleVerified} onCancel={handleCancelVerify} />
      </AuthScreen>
    );
  }
  if (user) {
    return <SessionContext value={{ user, signOut }}>{children}</SessionContext>;
  }
  return (
    <AuthScreen>
      {screen === 'login' ? (
        <LoginForm onSignedIn={setUser} onRegister={() => setScreen('register')} />
      ) : (
        <RegisterForm onSignIn={() => setScreen('login')} />
      )}
    </AuthScreen>
  );
}

function AuthScreen({ children }: Props) {
  return (
    <main className="auth-screen">
      <div className="auth-card">
        <h1>Track-a-doodle</h1>
        {children}
      </div>
    </main>
  );
}
