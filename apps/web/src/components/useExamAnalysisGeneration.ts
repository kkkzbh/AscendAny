import {
  observeExamAnalysisGeneration,
  type BrowserSession,
  type ExamAnalysisGeneration,
  type ExamAnalysisGenerationConnectionState,
  type ExamAnalysisGenerationEvent,
} from "@ascendany/sdk";
import { useCallback, useEffect, useRef, useState } from "react";
import { apiFailureMessage } from "../api/client";

export interface ExamAnalysisGenerationViewState {
  generation: ExamAnalysisGeneration | null;
  events: ExamAnalysisGenerationEvent[];
  connectionState: ExamAnalysisGenerationConnectionState | null;
  loading: boolean;
  error: string | null;
  retry: () => void;
}

interface InternalState extends Omit<ExamAnalysisGenerationViewState, "retry"> {
  examId: string;
}

function initialState(examId: string): InternalState {
  return {
    examId,
    generation: null,
    events: [],
    connectionState: null,
    loading: true,
    error: null,
  };
}

export function useExamAnalysisGeneration(
  session: BrowserSession,
  examId: string,
): ExamAnalysisGenerationViewState {
  const [state, setState] = useState<InternalState>(() => initialState(examId));
  const [retryRevision, setRetryRevision] = useState(0);
  const observedExamId = useRef(examId);
  const generationId = useRef<string | undefined>(undefined);
  const lastSequence = useRef(0);

  useEffect(() => {
    if (observedExamId.current !== examId) {
      observedExamId.current = examId;
      generationId.current = undefined;
      lastSequence.current = 0;
      setState(initialState(examId));
    } else {
      setState((current) => ({
        ...current,
        loading: current.generation === null,
        error: null,
      }));
    }

    let active = true;
    const controller = new AbortController();
    void observeExamAnalysisGeneration({
      session,
      examId,
      signal: controller.signal,
      resume: {
        generationId: generationId.current,
        afterSequence: lastSequence.current,
      },
      onGeneration: (generation, resetEvents) => {
        if (!active) return;
        generationId.current = generation.generationId;
        if (resetEvents) lastSequence.current = 0;
        setState((current) => ({
          examId,
          generation,
          events: resetEvents ? [] : current.events,
          connectionState: current.connectionState,
          loading: false,
          error: null,
        }));
      },
      onEvent: (event) => {
        if (!active) return;
        lastSequence.current = event.sequence;
        setState((current) => ({ ...current, events: [...current.events, event] }));
      },
      onConnectionState: (connectionState) => {
        if (!active) return;
        setState((current) => ({ ...current, connectionState }));
      },
    }).catch((observeError: unknown) => {
      if (!active || controller.signal.aborted) return;
      setState((current) => ({
        ...current,
        connectionState: null,
        loading: false,
        error: apiFailureMessage(observeError),
      }));
    });

    return () => {
      active = false;
      controller.abort();
    };
  }, [examId, retryRevision, session]);

  const retry = useCallback(() => {
    setRetryRevision((current) => current + 1);
  }, []);

  const visibleState = state.examId === examId ? state : initialState(examId);
  return { ...visibleState, retry };
}
