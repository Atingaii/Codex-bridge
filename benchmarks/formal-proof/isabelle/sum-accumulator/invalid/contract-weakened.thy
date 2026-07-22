theory Reference
  imports Main
begin

fun sum_acc :: "nat list \<Rightarrow> nat \<Rightarrow> nat" where
  "sum_acc [] accumulator = accumulator"
| "sum_acc (value # remaining) accumulator =
     sum_acc remaining (value + accumulator)"

theorem sum_acc_correct:
  "length xs = length xs"
  by simp

end
