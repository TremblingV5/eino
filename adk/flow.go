(some characters truncated)...
 a.Agent.Run(withCancelContext(ctx, cancelCtx), input, filterOptions(agentName, opts)...)

        iterator, generator := NewAsyncIteratorPair[*AgentEvent]()

        go a.run(withCancelContext(ctx, cancelCtx), withCancelContext(ctxForSubAgents, cancelCtx), runCtx, aIter, generator, filterCancelOption(opts)...)

        return wrapIterWithCancelCtx(iterator, cancelCtx)
}

func (a *flowAgent) Resume(ctx context.Context, info *ResumeInfo, opts ...AgentRunOption) *AsyncIterator[*AgentEvent] {
        agentName := a.Name(ctx)

        ctx, info = buildResumeInfo(ctx, agentName, info)

        ctxForSubAgents := ctx

        o := getCommonOptions(nil, opts...)
        cancelCtx := o.cancelCtx

        agentType := getAgentType(a.Agent)
        ctx = initAgentCallbacks(ctx, agentName, agentType, filterOptions(agentName, opts)...)
        cbInput := &AgentCallbackInput{ResumeInfo: info}
        ctx = callbacks.OnStart(ctx, cbInput)

        if info.WasInterrupted {
                if ra, ok := a.Agent.(ResumableAgent); ok {
                        if _, ok := ra.(*workflowAgent); ok {
                                ctx = withCancelContext(ctx, cancelCtx)
                                filteredOpts := filterCancelOption(filterCallbackHandlersForNestedAgents(agentName, opts))
                                aIter := ra.Resume(ctx, info, filteredOpts...)
                                aIter = wrapIterWithCancelCtx(aIter, cancelCtx)
                                return wrapIterWithOnEnd(ctx, aIter)
                        }

                        aIter := ra.Resume(withCancelContext(ctx, cancelCtx), info, opts...)

                        iterator, generator := NewAsyncIteratorPair[*AgentEvent]()
                        go a.run(withCancelContext(ctx, cancelCtx), withCancelContext(ctxForSubAgents, cancelCtx), getRunCtx(ctxForSubAgents), aIter, generator, filterCancelOption(opts)...)
                        return wrapIterWithCancelCtx(iterator, cancelCtx)
                }

                if cancelCtx != nil {
                        cancelCtx.markDone()
                }
                return wrapIterWithOnEnd(ctx, genErrorIter(fmt.Errorf("failed to resume agent: agent '%s' is an interrupt point "+
                        "but is not a ResumableAgent", agentName)))
        }

        nextAgentName, err := getNextResumeAgent(ctx, info)
        if err != nil {
                if cancelCtx != nil {
                        cancelCtx.markDone()
                }
                return wrapIterWithOnEnd(ctx, genErrorIter(err))
        }

        subAgent := a.getAgent(ctxForSubAgents, nextAgentName)
        if subAgent == nil {
                if len(a.subAgents) == 0 {
                        if ra, ok := a.Agent.(ResumableAgent); ok {
                                ctx = withCancelContext(ctx, cancelCtx)
                                innerIter := ra.Resume(ctx, info, filterCancelOption(opts)...)
                                return wrapIterWithCancelCtx(wrapIterWithOnEnd(ctx, innerIter), cancelCtx)
                        }
                        return wrapIterWithOnEnd(ctx, genErrorIter(fmt.Errorf(
                                "failed to resume agent: agent '%s' (type %T) has no sub-agents and does not implement ResumableAgent interface. "+
                                        "To support resume, your custom agent wrapper must implement the ResumableAgent interface", agentName, a.Agent)))
                }
                if cancelCtx != nil {
                        cancelCtx.markDone()
                }
                return wrapIterWithOnEnd(ctx, genErrorIter(fmt.Errorf("failed to resume agent: sub-agent '%s' not found in agent '%s'", nextAgentName, agentName)))
        }

        ctxForSubAgents = withCancelContext(ctxForSubAgents, cancelCtx)
        filteredOpts := filterCancelOption(opts)
        childCancelCtx := deriveCheckpointAwareSubAgentCancelContext(ctxForSubAgents, filteredOpts)
        childOpts := appendCancelContextOption(filteredOpts, childCancelCtx)
        innerIter := subAgent.Resume(ctxForSubAgents, info, childOpts...)
        return wrapIterWithCancelCtx(wrapIterWithOnEnd(ctx, innerIter), cancelCtx)
}

// DeterministicTransferConfig is the configuration for AgentWithDeterministicTransferTo.
//
// NOT RECOMMENDED: Agent transfer with full context sharing between agents has not proven
// to be more effective empirically. Consider using ChatModelAgent with AgentTool
// or DeepAgent instead for most multi-agent scenarios.
type DeterministicTransferConfig struct {
        Agent        Agent
        ToAgentNames []string
}

func (a *flowAgent) run(
        ctx context.Context,
        ctxForSubAgents context.Context,
        runCtx *runContext,
        aIter *AsyncIterator[*AgentEvent],
        generator *AsyncGenerator[*AgentEvent],
        opts ...AgentRunOption) {

        cbIter, cbGen := NewAsyncIteratorPair[*AgentEvent]()

        cbOutput := &AgentCallbackOutput{Events: cbIter}
        icb.On(ctx, cbOutput, icb.BuildOnEndHandleWithCopy(copyAgentCallbackOutput), callbacks.TimingOnEnd, false)

        defer func() {
                panicErr := recover()
                if panicErr != nil {
                        e := safe.NewPanicErr(panicErr, debug.Stack())
                        generator.Send(&AgentEvent{Err: e})
                }

                cbGen.Close()
                generator.Close()
        }()

        var lastAction *AgentAction
        for {
                event, ok := aIter.Next()
                if !ok {
                        break
                }

                // Reject nil events from custom agents at the central flow boundary
                // instead of relying on panic recovery. A nil event indicates a
                // contract violation by the custom Agent implementation.
                if event == nil {
                        generator.Send(&AgentEvent{Err: fmt.Errorf("agent '%s' returned nil event", a.Name(ctx))})
                        continue
                }

                // RunPath ownership: the eino framework sets RunPath exactly once.
                // If event.RunPath is already set (e.g., by agentTool), we don't modify it.
                // If event.RunPath is nil/empty, we set it to the current runCtx.RunPath.
                // This ensures RunPath is set exactly once and not duplicated.
                if len(event.RunPath) == 0 {
                        event.AgentName = a.Name(ctx)
                        event.RunPath = runCtx.RunPath
                }
                // Recording policy: exact RunPath match (non-interrupt) indicates events belonging to this agent execution.
                // This prevents parent recording of child/tool-internal emissions.
                if (event.Action == nil || event.Action.Interrupted == nil) && exactRunPathMatch(runCtx.RunPath, event.RunPath) {
                        // copy the event so that the copied event's stream is exclusive for any potential consumer
                        // copy before adding to session because once added to session it's stream could be consumed by genAgentInput at any time
                        // interrupt action are not added to session, because ALL information contained in it
                        // is either presented to end-user, or made available to agents through other means
                        copied := copyTypedAgentEvent(event)
                        setAutomaticClose(copied)
                        setAutomaticClose(event)
                        runCtx.Session.addEvent(copied)
                }
                // Action gating uses exact run-path match as well:
                // only actions originating from this agent execution (not child/tool runs)
                // should influence parent control flow (exit/transfer/interrupt).
                if exactRunPathMatch(runCtx.RunPath, event.RunPath) {
                        lastAction = event.Action
                }
                copied := copyTypedAgentEvent(event)
                setAutomaticClose(copied)
                setAutomaticClose(event)
                cbGen.Send(copied)
                generator.Send(event)
        }

        var destName string
        if lastAction != nil {
                if lastAction.Interrupted != nil {
                        return
                }
                if lastAction.Exit {
                        return
                }

                if lastAction.TransferToAgent != nil {
                        destName = lastAction.TransferToAgent.DestAgentName
                }
        }

        // handle transferring to another agent
        if destName != "" {
                agentToRun := a.getAgent(ctxForSubAgents, destName)
                if agentToRun == nil {
                        e := fmt.Errorf("transfer failed: agent '%s' not found when transferring from '%s'",
                                destName, a.Name(ctxForSubAgents))
                        generator.Send(&AgentEvent{Err: e})
                        return
                }

                childCancelCtx := deriveCheckpointAwareSubAgentCancelContext(ctxForSubAgents, opts)
                childOpts := appendCancelContextOption(opts, childCancelCtx)
                subAIter := agentToRun.Run(ctxForSubAgents, nil /*subagents get input from runCtx*/, childOpts...)
                for {
                        subEvent, ok_ := subAIter.Next()
                        if !ok_ {
                                break
                        }

                        setAutomaticClose(subEvent)
                        generator.Send(subEvent)
                }
        }
}

func exactRunPathMatch(aPath, bPath []RunStep) bool {
        if len(aPath) != len(bPath) {
                return false
        }
        for i := range aPath {
                if !aPath[i].Equals(bPath[i]) {
                        return false
                }
        }
        return true
}

func wrapIterWithOnEnd(ctx context.Context, iter *AsyncIterator[*AgentEvent]) *AsyncIterator[*AgentEvent] {
        cbIter, cbGen := NewAsyncIteratorPair[*AgentEvent]()
        cbOutput := &AgentCallbackOutput{Events: cbIter}
        icb.On(ctx, cbOutput, icb.BuildOnEndHandleWithCopy(copyAgentCallbackOutput), callbacks.TimingOnEnd, false)

        outIter, outGen := NewAsyncIteratorPair[*AgentEvent]()
        go func() {
                defer func() {
                        cbGen.Close()
                        outGen.Close()
                }()
                for {
                        event, ok := iter.Next()
                        if !ok {
                                break
                        }
                        copied := copyTypedAgentEvent(event)
                        cbGen.Send(copied)
                        outGen.Send(event)
                }
        }()
        return outIter
}

// ---------------------------------------------------------------------------
// Typed wrapper for the agentic path (TypedAgent[*schema.AgenticMessage]).
//
// typedFlowAgent is a minimal wrapper used exclusively by TypedRunner and
// AgentTool to execute a TypedAgent[*schema.AgenticMessage]. It handles
// callbacks, event recording, and run-path tracking. Transfer, sub-agent
// orchestration, and history rewriting are handled solely by the concrete
// flowAgent (the *schema.Message path).
// ---------------------------------------------------------------------------

type typedFlowAgent[M MessageType] struct {
        TypedAgent[M]

        checkPointStore compose.CheckPointStore
}

func toTypedFlowAgent[M MessageType](agent TypedAgent[M]) *typedFlowAgent[M] {
        if fa, ok := agent.(*typedFlowAgent[M]); ok {
                return fa
        }
        return &typedFlowAgent[M]{TypedAgent: agent}
}

func getTypedAgentType[M MessageType](agent TypedAgent[M]) string {
        if msgAgent, ok := any(agent).(Agent); ok {
                return getAgentType(msgAgent)
        }
        if typer, ok := any(agent).(interface{ GetType() string }); ok {
                return typer.GetType()
        }
        return ""
}

func (a *typedFlowAgent[M]) Run(ctx context.Context, input *TypedAgentInput[M], opts ...AgentRunOption) *AsyncIterator[*TypedAgentEvent[M]] {
        agentName := a.Name(ctx)

        var runCtx *runContext
        ctx, runCtx = initTypedRunCtx(ctx, agentName, input)
        ctx = AppendAddressSegment(ctx, AddressSegmentAgent, agentName)

        o := getCommonOptions(nil, opts...)
        cancelCtx := o.cancelCtx

        ctxForSubAgents := ctx

        agentType := getTypedAgentType(a.TypedAgent)
        ctx = initAgenticCallbacks(ctx, agentName, agentType, filterOptions(agentName, opts)...)
        cbInput := &TypedAgentCallbackInput[*schema.AgenticMessage]{Input: any(input).(*TypedAgentInput[*schema.AgenticMessage])}
        ctx = callbacks.OnStart(ctx, cbInput)

        aIter := a.TypedAgent.Run(withCancelContext(ctx, cancelCtx), input, filterOptions(agentName, opts)...)

        iterator, generator := NewAsyncIteratorPair[*TypedAgentEvent[M]]()

        go a.run(withCancelContext(ctx, cancelCtx), withCancelContext(ctxForSubAgents, cancelCtx), runCtx, aIter, generator, filterCancelOption(opts)...)

        return wrapIterWithCancelCtx(iterator, cancelCtx)
}

func (a *typedFlowAgent[M]) Resume(ctx context.Context, info *ResumeInfo, opts ...AgentRunOption) *AsyncIterator[*TypedAgentEvent[M]] {
        agentName := a.Name(ctx)

        ctx, info = buildResumeInfo(ctx, agentName, info)

        ctxForSubAgents := ctx

        o := getCommonOptions(nil, opts...)
        cancelCtx := o.cancelCtx

        agentType := getTypedAgentType(a.TypedAgent)
        ctx = initAgenticCallbacks(ctx, agentName, agentType, filterOptions(agentName, opts)...)
        cbInput := &TypedAgentCallbackInput[*schema.AgenticMessage]{ResumeInfo: info}
        ctx = callbacks.OnStart(ctx, cbInput)

        if info.WasInterrupted {
                if ra, ok := a.TypedAgent.(TypedResumableAgent[M]); ok {
                        aIter := ra.Resume(withCancelContext(ctx, cancelCtx), info, opts...)

                        iterator, generator := NewAsyncIteratorPair[*TypedAgentEvent[M]]()
                        go a.run(withCancelContext(ctx, cancelCtx), withCancelContext(ctxForSubAgents, cancelCtx), getRunCtx(ctxForSubAgents), aIter, generator, filterCancelOption(opts)...)
                        return wrapIterWithCancelCtx(iterator, cancelCtx)
                }

                if cancelCtx != nil {
                        cancelCtx.markDone()
                }
                return typedErrorIterWithOnEnd[M](ctx, fmt.Errorf("failed to resume agent: agent '%s' is an interrupt point "+
                        "but is not a ResumableAgent", agentName))
        }

        _, err := getNextResumeAgent(ctx, info)
        if err != nil {
                if cancelCtx != nil {
                        cancelCtx.markDone()
                }
                return typedErrorIterWithOnEnd[M](ctx, err)
        }

        if ra, ok := a.TypedAgent.(TypedResumableAgent[M]); ok {
                ctx = withCancelContext(ctx, cancelCtx)
                innerIter := ra.Resume(ctx, info, filterCancelOption(opts)...)
                return wrapIterWithCancelCtx(typedWrapIterWithOnEnd(ctx, innerIter), cancelCtx)
        }
        return typedErrorIterWithOnEnd[M](ctx, fmt.Errorf(
                "failed to resume agent: agent '%s' (type %T) does not implement ResumableAgent interface. "+
                        "To support resume, your custom agent wrapper must implement the ResumableAgent interface", agentName, a.TypedAgent))
}

func (a *typedFlowAgent[M]) run(
        ctx context.Context,
        _ context.Context,
        runCtx *runContext,
        aIter *AsyncIterator[*TypedAgentEvent[M]],
        generator *AsyncGenerator[*TypedAgentEvent[M]],
        _ ...AgentRunOption) {

        agenticCbIter, agenticCbGen := NewAsyncIteratorPair[*TypedAgentEvent[*schema.AgenticMessage]]()
        cbOutput := &TypedAgentCallbackOutput[*schema.AgenticMessage]{Events: agenticCbIter}
        icb.On(ctx, cbOutput, icb.BuildOnEndHandleWithCopy(copyTypedCallbackOutput[*schema.AgenticMessage]), callbacks.TimingOnEnd, false)

        defer func() {
                panicErr := recover()
                if panicErr != nil {
                        e := safe.NewPanicErr(panicErr, debug.Stack())
                        generator.Send(&TypedAgentEvent[M]{Err: e})
                }

                agenticCbGen.Close()
                generator.Close()
        }()

        for {
                event, ok := aIter.Next()
                if !ok {
                        break
                }

                // Reject nil events from custom agents at the central flow boundary
                // instead of relying on panic recovery. A nil event indicates a
                // contract violation by the custom Agent implementation.
                if event == nil {
                        generator.Send(&TypedAgentEvent[M]{Err: fmt.Errorf("agent '%s' returned nil event", a.Name(ctx))})
                        continue
                }

                if len(event.RunPath) == 0 {
                        event.AgentName = a.Name(ctx)
                        event.RunPath = runCtx.RunPath
                }
                if (event.Action == nil || event.Action.Interrupted == nil) && exactRunPathMatch(runCtx.RunPath, event.RunPath) {
                        copied := copyTypedAgentEvent(event)
                        typedSetAutomaticClose(copied)
                        typedSetAutomaticClose(event)
                        addTypedEvent(runCtx.Session, copied)
                }

                agenticCopied := copyTypedAgentEvent(event)
                typedSetAutomaticClose(agenticCopied)
                typedSetAutomaticClose(event)
                agenticCbGen.Send(any(agenticCopied).(*TypedAgentEvent[*schema.AgenticMessage]))
                generator.Send(event)
        }
}

func wrapAgenticIterWithOnEnd(ctx context.Context, iter *AsyncIterator[*TypedAgentEvent[*schema.AgenticMessage]]) *AsyncIterator[*TypedAgentEvent[*schema.AgenticMessage]] {
        cbIter, cbGen := NewAsyncIteratorPair[*TypedAgentEvent[*schema.AgenticMessage]]()
        cbOutput := &TypedAgentCallbackOutput[*schema.AgenticMessage]{Events: cbIter}
        icb.On(ctx, cbOutput, icb.BuildOnEndHandleWithCopy(copyTypedCallbackOutput[*schema.AgenticMessage]), callbacks.TimingOnEnd, false)

        outIter, outGen := NewAsyncIteratorPair[*TypedAgentEvent[*schema.AgenticMessage]]()
        go func() {
                defer func() {
                        cbGen.Close()
                        outGen.Close()
                }()
                for {
                        event, ok := iter.Next()
                        if !ok {
                                break
                        }
                        copied := copyTypedAgentEvent(event)
                        cbGen.Send(copied)
                        outGen.Send(event)
                }
        }()
        return outIter
}

func genAgenticErrorIter(err error) *AsyncIterator[*TypedAgentEvent[*schema.AgenticMessage]] {
        iter, gen := NewAsyncIteratorPair[*TypedAgentEvent[*schema.AgenticMessage]]()
        gen.Send(&TypedAgentEvent[*schema.AgenticMessage]{Err: err})
        gen.Close()
        return iter
}

func typedWrapIterWithOnEnd[M MessageType](ctx context.Context, iter *AsyncIterator[*TypedAgentEvent[M]]) *AsyncIterator[*TypedAgentEvent[M]] {
        agenticIter := any(iter).(*AsyncIterator[*TypedAgentEvent[*schema.AgenticMessage]])
        return any(wrapAgenticIterWithOnEnd(ctx, agenticIter)).(*AsyncIterator[*TypedAgentEvent[M]])
}

func typedErrorIterWithOnEnd[M MessageType](ctx context.Context, err error) *AsyncIterator[*TypedAgentEvent[M]] {
        return typedWrapIterWithOnEnd(ctx, typedErrorIter[M](err))
}