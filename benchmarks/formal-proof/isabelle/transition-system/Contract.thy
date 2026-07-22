theory Contract
  imports Reference
begin

lemma run_zero_contract:
  "run 0 state = state"
  by simp

lemma run_step_contract:
  "run (Suc steps) state = run steps (step state)"
  by simp

theorem run_busy_target_contract:
  "run steps (Busy counter) = Busy (counter + steps)"
  using run_busy_closed_form .

end
