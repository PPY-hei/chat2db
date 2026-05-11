import { create } from "zustand";
import { api, setToken } from "./api";
import type { User } from "./types";

interface AuthState {
  user: User | null;
  llmConfigured: boolean;
  loaded: boolean;
  login: (email: string, password: string) => Promise<void>;
  register: (email: string, name: string, password: string) => Promise<void>;
  logout: () => void;
  loadMe: () => Promise<void>;
}

export const useAuth = create<AuthState>((set, get) => ({
  user: null,
  llmConfigured: false,
  loaded: false,
  async login(email, password) {
    const { token } = await api.login(email, password);
    setToken(token);
    await get().loadMe();
  },
  async register(email, name, password) {
    const { token } = await api.register(email, name, password);
    setToken(token);
    await get().loadMe();
  },
  logout() {
    setToken(null);
    set({ user: null, llmConfigured: false });
    location.href = "/login";
  },
  async loadMe() {
    try {
      const me = await api.me();
      set({ user: me.user, llmConfigured: me.llm_configured, loaded: true });
    } catch {
      set({ user: null, loaded: true });
    }
  },
}));
