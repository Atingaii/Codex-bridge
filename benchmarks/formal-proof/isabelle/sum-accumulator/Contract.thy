theory Contract
  imports Reference
begin

lemma sum_acc_empty_contract:
  "sum_acc [] accumulator = accumulator"
  by simp

lemma sum_acc_step_contract:
  "sum_acc (value # xs) accumulator = sum_acc xs (value + accumulator)"
  by simp

theorem sum_acc_target_contract:
  "sum_acc xs accumulator = sum_list xs + accumulator"
  using sum_acc_correct .

end
