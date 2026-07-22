From Coq Require Import List.
Import ListNotations.
Require Import Reference.

Example reverse_accumulator_empty_contract :
  forall (A : Type) (accumulator : list A),
    @Reference.reverse_accumulator A [] accumulator = accumulator :=
  fun A accumulator => eq_refl.

Example reverse_accumulator_step_contract :
  forall (A : Type) (value : A) values accumulator,
    @Reference.reverse_accumulator A (value :: values) accumulator =
      Reference.reverse_accumulator values (value :: accumulator) :=
  fun A value values accumulator => eq_refl.

Definition reverse_accumulator_target_contract :
  forall (A : Type) (values accumulator : list A),
    Reference.reverse_accumulator values accumulator = rev values ++ accumulator :=
  Reference.reverse_accumulator_correct.

Print Assumptions Reference.reverse_accumulator_correct.
