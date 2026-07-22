theory Reference
  imports Main
begin

datatype machine_state = Idle | Busy nat

fun step :: "machine_state \<Rightarrow> machine_state" where
  "step Idle = Busy 0"
| "step (Busy counter) = Busy (Suc counter)"

fun run :: "nat \<Rightarrow> machine_state \<Rightarrow> machine_state" where
  "run 0 state = state"
| "run (Suc steps) state = run steps (step state)"

theorem run_busy_closed_form:
  "run steps (Busy counter) = Busy (counter + steps)"
  by (induction steps arbitrary: counter) auto

end
