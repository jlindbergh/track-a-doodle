import { useState, type FormEvent } from 'react';
import { register } from '../api';
import { useConfig } from '../config';
import { formatError } from '../format';

interface Props {
  onSignIn: () => void;
}

export default function RegisterForm({ onSignIn }: Props) {
  const { auth } = useConfig();
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // The address the confirmation email went to, once sent.
  const [sentTo, setSentTo] = useState<string | null>(null);

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await register(email, password);
      setSentTo(email.trim());
    } catch (err: unknown) {
      setError(formatError(err));
    }
    setBusy(false);
  }

  if (sentTo) {
    return (
      <div className="auth-form">
        <h2>Check your inbox</h2>
        <p>
          We sent an email to <strong>{sentTo}</strong>. Open the link in it and enter your password to finish signing
          up. The link works for 24 hours.
        </p>
        <p className="muted">
          No email after a few minutes? Check your spam folder, or{' '}
          <button type="button" className="link" onClick={() => setSentTo(null)}>
            sign up again
          </button>{' '}
          to get a new link.
        </p>
        <button type="button" className="secondary" onClick={onSignIn}>
          Back to sign in
        </button>
      </div>
    );
  }

  return (
    <form className="auth-form" onSubmit={handleSubmit}>
      <h2>Sign up</h2>
      <label>
        Email
        <input
          type="email"
          autoComplete="email"
          required
          autoFocus
          value={email}
          onChange={(e) => setEmail(e.target.value)}
        />
      </label>
      <label>
        Password
        <input
          type="password"
          autoComplete="new-password"
          required
          minLength={auth.passwordMinLength}
          maxLength={auth.passwordMaxLength}
          aria-describedby="password-hint"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
        />
        <span id="password-hint" className="hint">
          At least {auth.passwordMinLength} characters. A few words in a row work well.
        </span>
      </label>
      {error && <p className="error">{error}</p>}
      <button type="submit" className="primary" disabled={busy}>
        {busy ? 'Signing up…' : 'Sign up'}
      </button>
      <p className="auth-switch">
        Already have an account?{' '}
        <button type="button" className="link" onClick={onSignIn}>
          Sign in
        </button>
      </p>
    </form>
  );
}
