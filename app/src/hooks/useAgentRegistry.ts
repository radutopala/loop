import { useCallback, useRef, useState } from "react";
import type { WSEvent } from "../types";
import { useEventStream } from "./useEventStream";

export interface AgentInfo {
  agent_id: string;
  channel_id: string;
  name: string;
  status: string;
  work_summary: string;
}

interface AgentInstanceEventData {
  agent_id: string;
  channel_id: string;
  name?: string;
  status?: string;
  work_summary?: string;
}

interface UseAgentRegistryResult {
  agents: Map<string, AgentInfo>;
}

/**
 * Subscribes to agent_instance.* events and maintains a live map of agents.
 */
export function useAgentRegistry(channelId: string | null): UseAgentRegistryResult {
  const [agents, setAgents] = useState<Map<string, AgentInfo>>(new Map());
  const agentsRef = useRef(agents);
  agentsRef.current = agents;

  const onEvent = useCallback((event: WSEvent) => {
    const data = event.data as AgentInstanceEventData;
    if (!data?.agent_id) return;

    switch (event.type) {
      case "agent_instance.registered": {
        const next = new Map(agentsRef.current);
        next.set(data.agent_id, {
          agent_id: data.agent_id,
          channel_id: data.channel_id,
          name: data.name || data.agent_id,
          status: data.status || "idle",
          work_summary: data.work_summary || "",
        });
        setAgents(next);
        break;
      }
      case "agent_instance.unregistered": {
        const next = new Map(agentsRef.current);
        next.delete(data.agent_id);
        setAgents(next);
        break;
      }
      case "agent_instance.metadata": {
        const existing = agentsRef.current.get(data.agent_id);
        if (existing) {
          const next = new Map(agentsRef.current);
          next.set(data.agent_id, {
            ...existing,
            name: data.name || existing.name,
            status: data.status || existing.status,
            work_summary: data.work_summary ?? existing.work_summary,
          });
          setAgents(next);
        }
        break;
      }
    }
  }, []);

  useEventStream({ channelId, onEvent });

  return { agents };
}
