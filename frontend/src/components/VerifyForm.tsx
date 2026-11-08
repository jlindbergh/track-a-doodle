import { useState, type FormEvent } from 'react';
import { verify, type User } from '../api';
import { formatError } from '../format';

interface Props {
  /** The token from the emailed link. */
  token: string;
  onVerified: (user: User) => void;
  onCancel: () => void;
}

/**
 * Confirms a new account. Asking for the password chosen at sign-up means
 * only someone who knows it, and can read the address's email, can
 * activate the account.
 */
export default function VerifyForm({ token, onVerified, onCancel }: Props) {
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      onVerified(await verify(token, password));
    } catch (err: unknown) {
      setError(formatError(err));
      setBusy(false);
    }
  }

  return (
    <form className="auth-form" onSubmit={handleSubmit}>
      <h2>Confirm your account</h2>
      <p>Enter the password you chose when signing up.</p>
      <label>
        Password
        <input
          type="password"
          autoComplete="current-password"
          required
          autoFocus
          value={password}
          onChange={(e) => setPassword(e.target.value)}
        />
      </label>
      {error && <p className="error">{error}</p>}
      <button type="submit" className="primary" disabled={busy}>
        {busy ? 'Confirming…' : 'Confirm account'}
      </button>
      <p className="auth-switch">
        <button type="button" className="link" onClick={onCancel}>
          Go to sign in instead
        </button>
      </p>
    </form>
  );
}
