// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

import { useEffect, useRef, useState } from 'react';
import { AgentChat } from './AgentChat';
import type { Pending } from './AgentChat';

export const agentDrawerID = 'workspace-agent-drawer';

export interface AgentDrawerProps {
  token: string;
  userID: string;
  open: boolean;
  onClose: () => void;
  pendingTurns: Record<string, Pending>;
}

export function AgentDrawer(props: AgentDrawerProps) {
  return <AgentDrawerContents key={JSON.stringify([props.token, props.userID])} {...props} />;
}

function AgentDrawerContents({ token, open, onClose, pendingTurns }: AgentDrawerProps) {
  const [opened, setOpened] = useState(open);
  const closeButton = useRef<HTMLButtonElement>(null);

  if (open && !opened) setOpened(true);

  useEffect(() => {
    if (open) closeButton.current?.focus();
  }, [open]);

  useEffect(() => {
    if (!open) return;
    function handleEscape(event: KeyboardEvent) {
      if (event.key === 'Escape' && !event.defaultPrevented) {
        event.preventDefault();
        onClose();
      }
    }
    window.addEventListener('keydown', handleEscape);
    return () => window.removeEventListener('keydown', handleEscape);
  }, [open, onClose]);

  return (
    <aside
      id={agentDrawerID}
      className="workspace-agent-drawer"
      role="dialog"
      aria-label="Assistant"
      hidden={!open}
    >
      <button type="button" ref={closeButton} onClick={onClose}>Close assistant</button>
      {opened && <AgentChat token={token} pendingTurns={pendingTurns} visible={open} />}
    </aside>
  );
}
