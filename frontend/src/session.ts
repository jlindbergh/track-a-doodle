import { createContext, use } from 'react';
import type { User } from './api';

export interface Session {
  user: User;
  signOut: () => Promise<void>;
}

/** The signed-in session, provided by AuthGate. null in development mode. */
export const SessionContext = createContext<Session | null>(null);

/** Returns the signed-in session, or null when there is no sign-in (dev mode). */
export function useSession(): Session | null {
  return use(SessionContext);
}
